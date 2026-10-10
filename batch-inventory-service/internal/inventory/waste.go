package inventory

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Service) Waste(ctx context.Context, batchID, actor string, in WasteInput) (WasteRecord, error) {
	var err error
	if batchID, err = ID(batchID); err != nil {
		return WasteRecord{}, err
	}
	if actor, err = ID(actor); err != nil {
		return WasteRecord{}, err
	}
	if in.Key, err = ID(in.Key); err != nil {
		return WasteRecord{}, err
	}
	if in.Portions < 1 || in.Portions > MaxPortions || !validReason(in.Reason) || !shortText(in.Note, 500) {
		return WasteRecord{}, ErrInvalid
	}
	payload := struct {
		BatchID string
		Input   WasteInput
	}{batchID, in}
	return mutate(ctx, s, "waste:"+actor, in.Key, payload, func(tx pgx.Tx) (WasteRecord, error) {
		b, err := scanBatch(tx.QueryRow(ctx, "SELECT "+batchColumns+" FROM batches WHERE id=$1", batchID))
		if err != nil {
			return WasteRecord{}, err
		}
		if err = lockFlavors(ctx, tx, []string{b.FlavorID}); err != nil {
			return WasteRecord{}, err
		}
		b, err = scanBatch(tx.QueryRow(ctx, "SELECT "+batchColumns+" FROM batches WHERE id=$1 FOR UPDATE", batchID))
		if err != nil {
			return WasteRecord{}, err
		}
		t, err := now(ctx, tx)
		if err != nil {
			return WasteRecord{}, err
		}
		if b.Status == "ARCHIVED" {
			return WasteRecord{}, ErrState
		}
		if in.Reason == "EXPIRED" && b.ExpiresAt.After(t) {
			return WasteRecord{}, ErrInvalid
		}
		if b.Available < in.Portions {
			return WasteRecord{}, ErrStock
		}
		b.Available -= in.Portions
		b.Wasted += in.Portions
		result, err := s.recordWaste(ctx, tx, b, in.Portions, in.Reason, in.Note, actor, "", t)
		if err != nil {
			return result, err
		}
		return result, saveBatch(ctx, tx, b, t)
	})
}
func (s *Service) recordWaste(ctx context.Context, tx pgx.Tx, b Batch, n int64, reason, note, actor, reservation string, t time.Time) (WasteRecord, error) {
	w := WasteRecord{ID: uuid.NewString(), BatchID: b.ID, FlavorID: b.FlavorID, Portions: n, Reason: reason, Note: note, CostLost: b.UnitCost * n, Currency: "THB", RecordedAt: t}
	var who any
	if actor != "" {
		who = actor
	}
	_, err := tx.Exec(ctx, `INSERT INTO waste_records(id,batch_id,flavor_id,portions,reason,note,actor_id,cost_lost_minor,currency,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'THB',$9)`, w.ID, w.BatchID, w.FlavorID, w.Portions, w.Reason, w.Note, who, w.CostLost, t)
	if err != nil {
		return w, err
	}
	if err = movement(ctx, tx, b.ID, reservation, "WASTED", n, t); err != nil {
		return w, err
	}
	eventID := uuid.NewString()
	event := map[string]any{"specversion": "1.0", "id": eventID, "source": "/gelatoflow/batch-inventory-service", "type": "com.gelatoflow.inventory.waste-recorded.v1", "time": t.UTC().Format(time.RFC3339Nano), "datacontenttype": "application/json", "data": map[string]any{"waste_id": w.ID, "batch_id": b.ID, "flavor_id": b.FlavorID, "date": t.UTC().Format("2006-01-02"), "portions": n, "reason": reason, "cost_lost_minor": w.CostLost, "currency": "THB"}}
	body, err := json.Marshal(event)
	if err != nil {
		return w, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO outbox_events(id,payload,created_at) VALUES($1,$2,$3)", eventID, body, t)
	return w, err
}
