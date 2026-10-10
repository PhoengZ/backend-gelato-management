package inventory

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sort"
	"time"
)

func (s *Service) Reserve(ctx context.Context, in ReserveInput) (Reservation, error) {
	var err error
	in.Items = append([]Item{}, in.Items...)
	if err = validItems(in.Items); err != nil {
		return Reservation{}, err
	}
	if in.OrderID, err = ID(in.OrderID); err != nil {
		return Reservation{}, err
	}
	if in.Key, err = ID(in.Key); err != nil {
		return Reservation{}, err
	}
	sort.Slice(in.Items, func(i, j int) bool { return in.Items[i].FlavorID < in.Items[j].FlavorID })
	return mutate(ctx, s, "reserve", in.Key, in, func(tx pgx.Tx) (Reservation, error) {
		var r Reservation
		if err := lock(ctx, tx, "order:"+in.OrderID); err != nil {
			return r, err
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reservations WHERE order_id=$1)", in.OrderID).Scan(&exists); err != nil {
			return r, err
		}
		if exists {
			return r, ErrConflict
		}
		ids := []string{}
		for _, i := range in.Items {
			ids = append(ids, i.FlavorID)
		}
		if err := lockFlavors(ctx, tx, ids); err != nil {
			return r, err
		}
		t, err := now(ctx, tx)
		if err != nil {
			return r, err
		}
		if s.TTL <= 0 {
			return r, ErrInvalid
		}
		r = Reservation{ID: uuid.NewString(), OrderID: in.OrderID, Status: "ACTIVE", CreatedAt: t, ExpiresAt: t.Add(s.TTL), Allocations: []Allocation{}}
		batches := map[string]Batch{}
		for _, item := range in.Items {
			rows, err := tx.Query(ctx, `SELECT `+batchColumns+` FROM batches WHERE flavor_id=$1 AND status='ACTIVE' AND available_portions>0 AND expires_at>$2 ORDER BY expires_at,created_at,id FOR UPDATE`, item.FlavorID, t)
			if err != nil {
				return r, err
			}
			needed := item.Portions
			for rows.Next() {
				b, err := scanBatch(rows)
				if err != nil {
					rows.Close()
					return r, err
				}
				if needed == 0 {
					continue
				}
				take := min(needed, b.Available)
				b.Available -= take
				b.Reserved += take
				needed -= take
				batches[b.ID] = b
				r.Allocations = append(r.Allocations, Allocation{FlavorID: b.FlavorID, BatchID: b.ID, Portions: take})
				if b.ExpiresAt.Before(r.ExpiresAt) {
					r.ExpiresAt = b.ExpiresAt
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return r, err
			}
			if needed > 0 {
				return r, ErrStock
			}
		}
		// Recheck time after acquiring all row locks, not the transaction start time.
		current, err := now(ctx, tx)
		if err != nil {
			return r, err
		}
		if !r.ExpiresAt.After(current) {
			return r, ErrState
		}
		_, err = tx.Exec(ctx, "INSERT INTO reservations(id,order_id,status,created_at,expires_at,updated_at) VALUES($1,$2,'ACTIVE',$3,$4,$3)", r.ID, r.OrderID, r.CreatedAt, r.ExpiresAt)
		if err != nil {
			return r, err
		}
		for i, a := range r.Allocations {
			if err = saveBatch(ctx, tx, batches[a.BatchID], current); err != nil {
				return r, err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO reservation_allocations(reservation_id,batch_id,flavor_id,portions,position) VALUES($1,$2,$3,$4,$5)", r.ID, a.BatchID, a.FlavorID, a.Portions, i); err != nil {
				return r, err
			}
			if err = movement(ctx, tx, a.BatchID, r.ID, "RESERVED", a.Portions, current); err != nil {
				return r, err
			}
		}
		return r, nil
	})
}
func (s *Service) Transition(ctx context.Context, confirm bool, in TransitionInput) (Reservation, error) {
	var err error
	if in.ReservationID, err = ID(in.ReservationID); err != nil {
		return Reservation{}, err
	}
	if in.OrderID, err = ID(in.OrderID); err != nil {
		return Reservation{}, err
	}
	if in.Key, err = ID(in.Key); err != nil {
		return Reservation{}, err
	}
	if !shortText(in.Reason, 500) {
		return Reservation{}, ErrInvalid
	}
	op := "release"
	if confirm {
		op = "confirm"
		in.Reason = ""
	}
	return mutate(ctx, s, op, in.Key, in, func(tx pgx.Tx) (Reservation, error) { return s.transition(ctx, tx, in, confirm, false) })
}
func (s *Service) transition(ctx context.Context, tx pgx.Tx, in TransitionInput, confirm, expiry bool) (Reservation, error) {
	var r Reservation
	if err := lock(ctx, tx, "order:"+in.OrderID); err != nil {
		return r, err
	}
	ids, err := reservationFlavors(ctx, tx, in.ReservationID)
	if err != nil {
		return r, err
	}
	if err = lockFlavors(ctx, tx, ids); err != nil {
		return r, err
	}
	r, err = loadReservation(ctx, tx, in.ReservationID)
	if err != nil {
		return r, err
	}
	if r.OrderID != in.OrderID {
		return Reservation{}, ErrNotFound
	}
	t, err := now(ctx, tx)
	if err != nil {
		return r, err
	}
	if expiry && (r.Status != "ACTIVE" || r.ExpiresAt.After(t)) {
		return r, nil
	}
	if r.Status != "ACTIVE" {
		if (confirm && r.Status == "CONFIRMED") || (!confirm && (r.Status == "RELEASED" || r.Status == "EXPIRED")) {
			return r, nil
		}
		return r, ErrState
	}
	if confirm && !r.ExpiresAt.After(t) {
		return r, ErrState
	}
	target := "RELEASED"
	if confirm {
		target = "CONFIRMED"
	} else if expiry || !r.ExpiresAt.After(t) {
		target = "EXPIRED"
	}
	for _, a := range r.Allocations {
		b, err := scanBatch(tx.QueryRow(ctx, "SELECT "+batchColumns+" FROM batches WHERE id=$1 FOR UPDATE", a.BatchID))
		if err != nil {
			return r, err
		}
		if b.Reserved < a.Portions {
			return r, ErrState
		}
		b.Reserved -= a.Portions
		if confirm {
			if !b.ExpiresAt.After(t) || b.Status == "ARCHIVED" {
				return r, ErrState
			}
			b.Sold += a.Portions
			if err = movement(ctx, tx, b.ID, r.ID, "CONFIRMED", a.Portions, t); err != nil {
				return r, err
			}
		} else if !b.ExpiresAt.After(t) {
			b.Wasted += a.Portions
			if _, err = s.recordWaste(ctx, tx, b, a.Portions, "EXPIRED", "reservation expired after batch expiry", "", r.ID, t); err != nil {
				return r, err
			}
		} else {
			b.Available += a.Portions
			if err = movement(ctx, tx, b.ID, r.ID, "RELEASED", a.Portions, t); err != nil {
				return r, err
			}
		}
		if err = saveBatch(ctx, tx, b, t); err != nil {
			return r, err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE reservations SET status=$2,updated_at=$3,release_reason=$4 WHERE id=$1", r.ID, target, t, in.Reason); err != nil {
		return r, err
	}
	r.Status = target
	return r, nil
}

// Sweep is safe across replicas; discovery holds no row locks while acquiring
// order/flavor locks. Every candidate is rechecked inside its own transaction.
func (s *Service) Sweep(ctx context.Context, limit int) error {
	if limit < 1 || limit > 1000 {
		return ErrInvalid
	}
	rows, err := s.DB.Query(ctx, "SELECT id::text,order_id::text FROM reservations WHERE status='ACTIVE' AND expires_at<=clock_timestamp() ORDER BY expires_at,id LIMIT $1", limit)
	if err != nil {
		return err
	}
	inputs := []TransitionInput{}
	for rows.Next() {
		var i TransitionInput
		if err = rows.Scan(&i.ReservationID, &i.OrderID); err != nil {
			rows.Close()
			return err
		}
		i.Reason = "reservation timeout"
		inputs = append(inputs, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, i := range inputs {
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		_, err = s.transition(ctx, tx, i, false, true)
		if err == nil {
			err = tx.Commit(ctx)
		}
		rollback(tx)
		if err != nil {
			return err
		}
	}
	rows, err = s.DB.Query(ctx, "SELECT id::text,flavor_id::text FROM batches WHERE status<>'ARCHIVED' AND expires_at<=clock_timestamp() AND (available_portions>0 OR status<>'EXPIRED') ORDER BY expires_at,id LIMIT $1", limit)
	if err != nil {
		return err
	}
	type candidate struct{ id, flavor string }
	bs := []candidate{}
	for rows.Next() {
		var b candidate
		if err = rows.Scan(&b.id, &b.flavor); err != nil {
			rows.Close()
			return err
		}
		bs = append(bs, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, candidate := range bs {
		if err = s.expireBatch(ctx, candidate.id, candidate.flavor); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) expireBatch(ctx context.Context, id, flavor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = lockFlavors(ctx, tx, []string{flavor}); err != nil {
		return err
	}
	b, err := scanBatch(tx.QueryRow(ctx, "SELECT "+batchColumns+" FROM batches WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return err
	}
	t, err := now(ctx, tx)
	if err != nil {
		return err
	}
	if b.ExpiresAt.After(t) || b.Status == "ARCHIVED" {
		return nil
	}
	if b.Available > 0 {
		n := b.Available
		b.Available = 0
		b.Wasted += n
		if _, err = s.recordWaste(ctx, tx, b, n, "EXPIRED", "batch expiry", "", "", t); err != nil {
			return err
		}
	}
	if err = saveBatch(ctx, tx, b, t); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Check reads all requested flavors from one availability snapshot.
func (s *Service) Check(ctx context.Context, items []Item) ([]Availability, time.Time, error) {
	if err := validItems(items); err != nil {
		return nil, time.Time{}, err
	}
	all, t, err := s.Availability(ctx, "")
	if err != nil {
		return nil, t, err
	}
	byID := map[string]Availability{}
	for _, a := range all {
		byID[a.FlavorID] = a
	}
	out := []Availability{}
	for _, item := range items {
		a := byID[item.FlavorID]
		a.FlavorID = item.FlavorID
		out = append(out, a)
	}
	return out, t, nil
}
