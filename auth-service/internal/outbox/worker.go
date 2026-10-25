package outbox

import (
	"context"
	"errors"
	"time"

	"auth-service/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrStore     = errors.New("Auth outbox storage unavailable")
	ErrClaimLost = errors.New("Auth outbox claim superseded")
)

type Publisher interface {
	Publish(context.Context, Message) error
}

type Worker struct {
	db            *pgxpool.Pool
	publisher     Publisher
	poll, timeout time.Duration
}

func NewWorker(db *pgxpool.Pool, publisher Publisher, poll, timeout time.Duration) *Worker {
	return &Worker{db: db, publisher: publisher, poll: poll, timeout: timeout}
}

// DispatchOne leases an event in a short atomic statement, releasing all row
// locks and the pool connection before network I/O. A crashed attempt becomes
// eligible again; its monotonically increasing attempt fences stale completions.
func (w *Worker) DispatchOne(ctx context.Context) (bool, error) {
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var event model.OutboxEvent
	err := w.db.QueryRow(claimCtx, `
		UPDATE outbox_events SET attempts = attempts + 1,
		    next_attempt_at = clock_timestamp() + $1::bigint * interval '1 millisecond'
		WHERE id = (
		    SELECT id FROM outbox_events
		    WHERE published_at IS NULL AND next_attempt_at <= clock_timestamp()
		    ORDER BY next_attempt_at, created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		RETURNING id, event_type, payload, created_at, attempts`,
		(w.timeout+10*time.Second).Milliseconds()).Scan(&event.ID, &event.EventType, &event.Payload, &event.CreatedAt, &event.Attempts)
	cancel()
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrStore
	}
	message, publishErr := MessageFor(event)
	if publishErr == nil {
		publishCtx, cancelPublish := context.WithTimeout(ctx, w.timeout)
		publishErr = w.publisher.Publish(publishCtx, message)
		cancelPublish()
	}
	// Finish a bounded acknowledgement even when shutdown cancelled the attempt.
	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelFinish()
	tag, err := w.db.Exec(finishCtx, `
		UPDATE outbox_events
		SET published_at = CASE WHEN $3 THEN clock_timestamp() ELSE NULL END,
		    next_attempt_at = clock_timestamp() + $4::bigint * interval '1 millisecond'
		WHERE id = $1 AND attempts = $2 AND published_at IS NULL`,
		event.ID, event.Attempts, publishErr == nil, retryDelay(event.Attempts).Milliseconds())
	if err != nil {
		return true, ErrStore
	}
	if tag.RowsAffected() != 1 {
		return true, ErrClaimLost
	}
	return true, publishErr
}

func retryDelay(attempt int) time.Duration {
	return min(time.Duration(1<<min(max(attempt-1, 0), 9))*time.Second, 5*time.Minute)
}

func (w *Worker) Run(ctx context.Context, report func(error)) {
	for ctx.Err() == nil {
		worked, err := w.DispatchOne(ctx)
		if err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(w.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Category contains fixed diagnostics only; raw DB/AMQP errors and payloads stay private.
func Category(err error) string {
	switch {
	case errors.Is(err, ErrStore):
		return "storage_unavailable"
	case errors.Is(err, ErrInvalidEvent):
		return "invalid_event"
	case errors.Is(err, ErrUnroutable):
		return "unroutable"
	case errors.Is(err, ErrClaimLost):
		return "claim_superseded"
	default:
		return "delivery_unconfirmed"
	}
}
