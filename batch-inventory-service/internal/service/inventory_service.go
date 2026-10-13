// Package service provides domain logic for inventory thresholds, alerts, and notifications.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// LowStockThreshold is the portion limit at or below which low stock notifications fire.
	LowStockThreshold int64 = 10

	// ExpiryAlertWindow is the time duration before batch expiry when alerts fire (48 hours).
	ExpiryAlertWindow = 48 * time.Hour

	// Event Source
	EventSource = "/gelatoflow/batch-inventory-service"

	// CloudEvent Types
	TypeLowStock      = "com.gelatoflow.inventory.batch-low-stock.v1"
	TypeExpiring      = "com.gelatoflow.inventory.batch-expiring.v1"
	TypeWasteRecorded = "com.gelatoflow.inventory.waste-recorded.v1"

	// Routing Keys
	RoutingKeyLowStock = "inventory.low_stock"
	RoutingKeyExpiring = "inventory.expiring"
	RoutingKeyWaste    = "inventory.waste"
)

// Advisory lock for flavor serialization
func lockFlavor(ctx context.Context, tx pgx.Tx, flavorID string) error {
	sum := sha256.Sum256([]byte("flavor:" + flavorID))
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(sum[:8])))
	return err
}

// CheckAndNotifyLowStock inspects the total available portions for a flavor.
// If available <= LowStockThreshold (10 portions), it publishes an 'inventory.low_stock' CloudEvent
// into the outbox if a notification has not already been sent.
// If available > LowStockThreshold, it resets the notification state to allow future alerts.
func CheckAndNotifyLowStock(ctx context.Context, tx pgx.Tx, flavorID string, t time.Time) (bool, error) {
	if flavorID == "" {
		return false, nil
	}

	var totalAvailable int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(available_portions), 0)::bigint
		FROM batches
		WHERE flavor_id = $1 AND status = 'ACTIVE' AND expires_at > $2
	`, flavorID, t).Scan(&totalAvailable)
	if err != nil {
		return false, err
	}

	// If stock has been replenished above threshold, reset notification flag to prevent alert suppression later.
	if totalAvailable > LowStockThreshold {
		_, err = tx.Exec(ctx, `
			UPDATE batches
			SET low_stock_notified_at = NULL
			WHERE flavor_id = $1 AND low_stock_notified_at IS NOT NULL
		`, flavorID)
		return false, err
	}

	// Check if already notified for this flavor while in low-stock state
	var notifiedCount int64
	err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM batches
		WHERE flavor_id = $1 AND low_stock_notified_at IS NOT NULL
	`, flavorID).Scan(&notifiedCount)
	if err != nil {
		return false, err
	}

	if notifiedCount > 0 {
		// Already notified, prevent alert storming
		return false, nil
	}

	// Find the most recent batch ID for reference in the event
	var batchID string
	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM batches
		WHERE flavor_id = $1
		ORDER BY expires_at DESC, created_at DESC
		LIMIT 1
	`, flavorID).Scan(&batchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	// Mark all batches of this flavor as notified
	_, err = tx.Exec(ctx, `
		UPDATE batches
		SET low_stock_notified_at = $2
		WHERE flavor_id = $1
	`, flavorID, t)
	if err != nil {
		return false, err
	}

	// Insert CloudEvent into outbox_events
	eventID := uuid.NewString()
	event := map[string]any{
		"specversion":     "1.0",
		"id":              eventID,
		"source":          EventSource,
		"type":            TypeLowStock,
		"time":            t.UTC().Format(time.RFC3339Nano),
		"datacontenttype": "application/json",
		"data": map[string]any{
			"batch_id":           batchID,
			"flavor_id":          flavorID,
			"remaining_portions": totalAvailable,
			"threshold_portions": LowStockThreshold,
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return false, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (id, payload, created_at)
		VALUES ($1, $2, $3)
	`, eventID, payload, t)
	if err != nil {
		return false, err
	}

	return true, nil
}

// ResetLowStockNotified resets the low stock notification state for a flavor if total stock exceeds threshold.
func ResetLowStockNotified(ctx context.Context, tx pgx.Tx, flavorID string, t time.Time) error {
	var totalAvailable int64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(available_portions), 0)::bigint
		FROM batches
		WHERE flavor_id = $1 AND status = 'ACTIVE' AND expires_at > $2
	`, flavorID, t).Scan(&totalAvailable)
	if err != nil {
		return err
	}

	if totalAvailable > LowStockThreshold {
		_, err = tx.Exec(ctx, `
			UPDATE batches
			SET low_stock_notified_at = NULL
			WHERE flavor_id = $1 AND low_stock_notified_at IS NOT NULL
		`, flavorID)
		return err
	}
	return nil
}

// NotifyExpiringBatches discovers active batches within 48h of expiration that have not yet been notified,
// and inserts 'inventory.expiring' CloudEvents into outbox_events.
func NotifyExpiringBatches(ctx context.Context, db *pgxpool.Pool, limit int) (int, error) {
	if limit < 1 {
		limit = 100
	}

	rows, err := db.Query(ctx, `
		SELECT id::text, flavor_id::text
		FROM batches
		WHERE status = 'ACTIVE'
		  AND available_portions > 0
		  AND expires_at > clock_timestamp()
		  AND expires_at <= clock_timestamp() + interval '48 hours'
		  AND expiring_notified_at IS NULL
		ORDER BY expires_at, id
		LIMIT $1
	`, limit)
	if err != nil {
		return 0, err
	}

	type candidate struct {
		id       string
		flavorID string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.flavorID); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	notified := 0
	for _, c := range candidates {
		ok, err := notifyExpiringBatch(ctx, db, c.id, c.flavorID)
		if err != nil {
			return notified, err
		}
		if ok {
			notified++
		}
	}
	return notified, nil
}

func notifyExpiringBatch(ctx context.Context, db *pgxpool.Pool, batchID, flavorID string) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()

	if err = lockFlavor(ctx, tx, flavorID); err != nil {
		return false, err
	}

	var expiresAt time.Time
	var t time.Time
	err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&t)
	if err != nil {
		return false, err
	}
	t = t.UTC()

	err = tx.QueryRow(ctx, `
		SELECT expires_at
		FROM batches
		WHERE id = $1
		  AND status = 'ACTIVE'
		  AND available_portions > 0
		  AND expires_at > $2
		  AND expires_at <= $2 + interval '48 hours'
		  AND expiring_notified_at IS NULL
		FOR UPDATE
	`, batchID, t).Scan(&expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE batches
		SET expiring_notified_at = $2
		WHERE id = $1
	`, batchID, t)
	if err != nil {
		return false, err
	}

	eventID := uuid.NewString()
	event := map[string]any{
		"specversion":     "1.0",
		"id":              eventID,
		"source":          EventSource,
		"type":            TypeExpiring,
		"time":            t.UTC().Format(time.RFC3339Nano),
		"datacontenttype": "application/json",
		"data": map[string]any{
			"batch_id":   batchID,
			"flavor_id":  flavorID,
			"expires_at": expiresAt.UTC().Format(time.RFC3339),
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return false, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (id, payload, created_at)
		VALUES ($1, $2, $3)
	`, eventID, payload, t)
	if err != nil {
		return false, err
	}

	return true, tx.Commit(ctx)
}
