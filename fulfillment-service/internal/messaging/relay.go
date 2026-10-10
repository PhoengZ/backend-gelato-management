package messaging

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	relayInterval  = 2 * time.Second
	relayBatchSize = 50
)

// Relay publishes unsent outbox rows. Rows are marked published only after the
// broker confirms, so a crash causes a re-send (at-least-once), never a loss.
type Relay struct {
	db     *pgxpool.Pool
	broker *Broker
}

func NewRelay(db *pgxpool.Pool, broker *Broker) *Relay {
	return &Relay{db: db, broker: broker}
}

// Run flushes the outbox until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(relayInterval)
	defer ticker.Stop()

	for {
		if err := r.flush(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[outbox] flush failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) flush(ctx context.Context) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, routing_key, payload FROM outbox
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, relayBatchSize)
	if err != nil {
		return err
	}

	type row struct {
		id      uuid.UUID
		key     string
		payload []byte
	}
	var pending []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.key, &x.payload); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, x := range pending {
		if err := r.broker.Publish(ctx, x.key, x.payload); err != nil {
			// Commit what was already confirmed, retry the rest next tick.
			if cerr := tx.Commit(ctx); cerr != nil {
				return cerr
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = $1`, x.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
