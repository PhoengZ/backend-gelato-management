package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
	"time"
)

type Catalog interface {
	CheckActive(context.Context, string) error
}
type Service struct {
	DB      *pgxpool.Pool
	Catalog Catalog
	TTL     time.Duration
}

func New(db *pgxpool.Pool, catalog Catalog, ttl time.Duration) *Service {
	return &Service{DB: db, Catalog: catalog, TTL: ttl}
}

// All stock writers take the same transaction-scoped flavor locks, including
// batch creation and expiry. This also serializes changes when no batch exists.
// Order lock precedes sorted flavor locks; no path acquires them in reverse.
func lock(ctx context.Context, tx pgx.Tx, key string) error {
	sum := sha256.Sum256([]byte(key))
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(sum[:8])))
	return err
}
func lockFlavors(ctx context.Context, tx pgx.Tx, ids []string) error {
	ids = append([]string{}, ids...)
	sort.Strings(ids)
	prev := ""
	for _, id := range ids {
		if id == prev {
			continue
		}
		if err := lock(ctx, tx, "flavor:"+id); err != nil {
			return err
		}
		prev = id
	}
	return nil
}
func now(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var t time.Time
	err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&t)
	return t.UTC(), err
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// Claim, mutation, and successful response commit atomically. A concurrent retry
// waits for the unique key, then replays the exact original response after commit.
func mutate[T any](ctx context.Context, s *Service, op, key string, input any, fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer rollback(tx)
	body, err := json.Marshal(input)
	if err != nil {
		return zero, err
	}
	hash := sha256.Sum256(body)
	tag, err := tx.Exec(ctx, "INSERT INTO idempotency_records(operation,key,request_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", op, key, hash[:])
	if err != nil {
		return zero, err
	}
	if tag.RowsAffected() == 0 {
		var savedHash, response []byte
		if err = tx.QueryRow(ctx, "SELECT request_hash,response FROM idempotency_records WHERE operation=$1 AND key=$2", op, key).Scan(&savedHash, &response); err != nil {
			return zero, err
		}
		if !bytes.Equal(hash[:], savedHash) {
			return zero, ErrConflict
		}
		if err = json.Unmarshal(response, &zero); err != nil {
			return zero, err
		}
		return zero, nil
	}
	result, err := fn(tx)
	if err != nil {
		return zero, err
	}
	response, err := json.Marshal(result)
	if err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, "UPDATE idempotency_records SET response=$3 WHERE operation=$1 AND key=$2", op, key, response); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return result, nil
}

const batchColumns = `id::text,flavor_id::text,production_date::text,expires_at,initial_portions,available_portions,reserved_portions,sold_portions,wasted_portions,unit_cost_minor,currency,status,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanBatch(row scanner) (Batch, error) {
	var b Batch
	err := row.Scan(&b.ID, &b.FlavorID, &b.ProductionDate, &b.ExpiresAt, &b.Initial, &b.Available, &b.Reserved, &b.Sold, &b.Wasted, &b.UnitCost, &b.Currency, &b.Status, &b.CreatedAt, &b.UpdatedAt)
	if err == pgx.ErrNoRows {
		err = ErrNotFound
	}
	return b, err
}
func batchStatus(b Batch, t time.Time) string {
	if b.Status == "ARCHIVED" {
		return "ARCHIVED"
	}
	if !b.ExpiresAt.After(t) {
		return "EXPIRED"
	}
	if b.Available == 0 {
		return "EXHAUSTED"
	}
	return "ACTIVE"
}
func saveBatch(ctx context.Context, tx pgx.Tx, b Batch, t time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE batches SET available_portions=$2,reserved_portions=$3,sold_portions=$4,wasted_portions=$5,status=$6,updated_at=$7 WHERE id=$1`, b.ID, b.Available, b.Reserved, b.Sold, b.Wasted, batchStatus(b, t), t)
	return err
}
func movement(ctx context.Context, tx pgx.Tx, batch, res, kind string, n int64, t time.Time) error {
	var rid any
	if res != "" {
		rid = res
	}
	_, err := tx.Exec(ctx, "INSERT INTO inventory_movements(id,batch_id,reservation_id,kind,portions,occurred_at) VALUES($1,$2,$3,$4,$5,$6)", uuid.NewString(), batch, rid, kind, n, t)
	return err
}
func loadReservation(ctx context.Context, tx pgx.Tx, id string) (Reservation, error) {
	var r Reservation
	err := tx.QueryRow(ctx, "SELECT id::text,order_id::text,status,created_at,expires_at FROM reservations WHERE id=$1 FOR UPDATE", id).Scan(&r.ID, &r.OrderID, &r.Status, &r.CreatedAt, &r.ExpiresAt)
	if err == pgx.ErrNoRows {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	rows, err := tx.Query(ctx, "SELECT flavor_id::text,batch_id::text,portions FROM reservation_allocations WHERE reservation_id=$1 ORDER BY position", id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Allocations = []Allocation{}
	for rows.Next() {
		var a Allocation
		if err = rows.Scan(&a.FlavorID, &a.BatchID, &a.Portions); err != nil {
			return r, err
		}
		r.Allocations = append(r.Allocations, a)
	}
	return r, rows.Err()
}
func reservationFlavors(ctx context.Context, tx pgx.Tx, id string) ([]string, error) {
	rows, err := tx.Query(ctx, "SELECT DISTINCT flavor_id::text FROM reservation_allocations WHERE reservation_id=$1", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func wrapInvalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }
