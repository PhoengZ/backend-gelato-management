package messaging

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Publisher interface {
	Publish(context.Context, string, []byte) error
}

// DispatchOne locks only an outbox row, never a stock row. A broker ACK followed
// by a DB failure can cause redelivery with the same event ID: at-least-once.
func DispatchOne(ctx context.Context, db *pgxpool.Pool, p Publisher) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	var id string
	var payload []byte
	var attempts int
	err = tx.QueryRow(ctx, `SELECT id::text,payload,attempts FROM outbox_events WHERE published_at IS NULL AND next_attempt_at<=clock_timestamp() ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &payload, &attempts)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = p.Publish(ctx, id, payload); err != nil {
		delay := time.Duration(1<<min(attempts, 8)) * time.Second
		if _, dbErr := tx.Exec(ctx, "UPDATE outbox_events SET attempts=attempts+1,next_attempt_at=clock_timestamp()+$2::bigint*interval '1 millisecond' WHERE id=$1", id, delay.Milliseconds()); dbErr != nil {
			return true, dbErr
		}
		if dbErr := tx.Commit(ctx); dbErr != nil {
			return true, dbErr
		}
		return true, err
	}
	if _, err = tx.Exec(ctx, "UPDATE outbox_events SET published_at=clock_timestamp(),attempts=attempts+1 WHERE id=$1", id); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}
