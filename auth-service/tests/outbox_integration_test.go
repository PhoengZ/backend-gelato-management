package tests

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"auth-service/internal/model"
	"auth-service/internal/outbox"
	"auth-service/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type authPublisherFunc func(context.Context, outbox.Message) error

func (f authPublisherFunc) Publish(ctx context.Context, m outbox.Message) error { return f(ctx, m) }

func seedAuthOutbox(t *testing.T, db *pgxpool.Pool) *model.OutboxEvent {
	t.Helper()
	u := &model.User{ID: uuid.New(), Email: uuid.NewString() + "@example.invalid", DisplayName: "Private Auth User", PasswordHash: "private-password-hash", Role: model.RoleCustomer, Status: model.UserStatusActive}
	e := &model.OutboxEvent{ID: uuid.New(), EventType: outbox.RoutingKey, Payload: []byte(`{"user_id":"` + u.ID.String() + `","role":"CUSTOMER","status":"ACTIVE","email":"private@example.invalid","display_name":"Private Auth User"}`)}
	if err := repository.NewPostgresUserRepository(db).CreateWithOutbox(context.Background(), u, e); err != nil {
		t.Fatal(err)
	}
	return e
}

func makeAuthOutboxDue(t *testing.T, db *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := db.Exec(context.Background(), "UPDATE outbox_events SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
}

func authOutboxState(t *testing.T, db *pgxpool.Pool, id uuid.UUID) (bool, int, float64) {
	t.Helper()
	var published bool
	var attempts int
	var delay float64
	if err := db.QueryRow(context.Background(), `SELECT published_at IS NOT NULL, attempts, extract(epoch FROM next_attempt_at-clock_timestamp())::double precision FROM outbox_events WHERE id=$1`, id).Scan(&published, &attempts, &delay); err != nil {
		t.Fatal(err)
	}
	return published, attempts, delay
}

func TestAuthOutboxConcurrentClaimDoesNotHoldDatabaseLocksDuringPublish(t *testing.T) {
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	worker := outbox.NewWorker(db, authPublisherFunc(func(ctx context.Context, m outbox.Message) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}), 50*time.Millisecond, 5*time.Second)
	done := make(chan error, 1)
	go func() { _, err := worker.DispatchOne(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher never started")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func() {
			worked, err := worker.DispatchOne(context.Background())
			if worked {
				err = errors.New("same pending event claimed twice")
			}
			results <- err
		}()
	}
	for i := 0; i < 20; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	// A broker wait must hold neither the event row lock nor the signup transaction.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, "UPDATE outbox_events SET event_type=event_type WHERE id=$1", e.ID); err != nil {
		t.Fatal("broker held a DB row lock", err)
	}
	seedAuthOutbox(t, db)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	published, attempts, _ := authOutboxState(t, db, e.ID)
	if !published || attempts != 1 || calls.Load() != 1 {
		t.Fatal("concurrent claim did not preserve one confirmed delivery")
	}
}

func TestAuthOutboxRetryPersistsBackoffAndOriginalEventAcrossWorkerRestart(t *testing.T) {
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	var original outbox.Message
	failing := outbox.NewWorker(db, authPublisherFunc(func(_ context.Context, m outbox.Message) error { original = m; return outbox.ErrPublish }), 50*time.Millisecond, time.Second)
	for i := 1; i <= 3; i++ {
		if worked, err := failing.DispatchOne(context.Background()); !worked || !errors.Is(err, outbox.ErrPublish) {
			t.Fatal("failure was acknowledged", err)
		}
		published, attempts, delay := authOutboxState(t, db, e.ID)
		want := float64(int64(1) << (i - 1))
		if published || attempts != i || delay < want-.5 || delay > want+.5 {
			t.Fatal("retry state/backoff was not persisted", attempts, delay)
		}
		if worked, err := failing.DispatchOne(context.Background()); worked || err != nil {
			t.Fatal("backoff was ignored", err)
		}
		makeAuthOutboxDue(t, db, e.ID)
	}
	restarted := outbox.NewWorker(db, authPublisherFunc(func(_ context.Context, m outbox.Message) error {
		if m.ID != original.ID || !m.CreatedAt.Equal(original.CreatedAt) || !bytes.Equal(m.Body, original.Body) {
			t.Error("retry changed original message")
		}
		return nil
	}), 50*time.Millisecond, time.Second)
	if worked, err := restarted.DispatchOne(context.Background()); !worked || err != nil {
		t.Fatal(err)
	}
	if published, attempts, _ := authOutboxState(t, db, e.ID); !published || attempts != 4 {
		t.Fatal("restart lost pending event")
	}
}

func TestAuthOutboxExpiredClaimFencesStaleCompletion(t *testing.T) {
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	second := outbox.NewWorker(db, authPublisherFunc(func(context.Context, outbox.Message) error { return nil }), 50*time.Millisecond, time.Second)
	first := outbox.NewWorker(db, authPublisherFunc(func(_ context.Context, m outbox.Message) error {
		makeAuthOutboxDue(t, db, e.ID) // model suspension/crash beyond the persisted lease
		if worked, err := second.DispatchOne(context.Background()); !worked || err != nil {
			t.Fatal(err)
		}
		return nil
	}), 50*time.Millisecond, time.Second)
	if _, err := first.DispatchOne(context.Background()); !errors.Is(err, outbox.ErrClaimLost) {
		t.Fatal("stale worker overwrote the new attempt", err)
	}
	if published, attempts, _ := authOutboxState(t, db, e.ID); !published || attempts != 2 {
		t.Fatal("latest confirmed attempt lost")
	}
}

func TestAuthOutboxBrokerAckThenDatabaseFailureReplaysSameID(t *testing.T) {
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	if _, err := db.Exec(context.Background(), `CREATE FUNCTION reject_publish_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.published_at IS NOT NULL THEN RAISE EXCEPTION 'simulated storage failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_ack BEFORE UPDATE ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_publish_ack()`); err != nil {
		t.Fatal(err)
	}
	var deliveries []outbox.Message
	worker := outbox.NewWorker(db, authPublisherFunc(func(_ context.Context, m outbox.Message) error { deliveries = append(deliveries, m); return nil }), 50*time.Millisecond, time.Second)
	if _, err := worker.DispatchOne(context.Background()); !errors.Is(err, outbox.ErrStore) {
		t.Fatal("ack was falsely persisted", err)
	}
	if published, attempts, delay := authOutboxState(t, db, e.ID); published || attempts != 1 || delay < 9 {
		t.Fatal("failed DB ack lost recoverable lease")
	}
	if _, err := db.Exec(context.Background(), `DROP TRIGGER reject_ack ON outbox_events`); err != nil {
		t.Fatal(err)
	}
	makeAuthOutboxDue(t, db, e.ID)
	if _, err := worker.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 || deliveries[0].ID != deliveries[1].ID || !bytes.Equal(deliveries[0].Body, deliveries[1].Body) {
		t.Fatal("ambiguous ack retry cannot be deduplicated")
	}
}

func TestAuthOutboxInvalidEventIsRetainedWithoutStarvingValidSignup(t *testing.T) {
	db := hardeningDatabase(t)
	invalid := seedAuthOutbox(t, db)
	if _, err := db.Exec(context.Background(), `UPDATE outbox_events SET event_type='unknown' WHERE id=$1`, invalid.ID); err != nil {
		t.Fatal(err)
	}
	valid := seedAuthOutbox(t, db)
	var calls int
	worker := outbox.NewWorker(db, authPublisherFunc(func(_ context.Context, m outbox.Message) error {
		calls++
		if m.ID != valid.ID {
			t.Error("published invalid event")
		}
		return nil
	}), 50*time.Millisecond, time.Second)
	if _, err := worker.DispatchOne(context.Background()); !errors.Is(err, outbox.ErrInvalidEvent) {
		t.Fatal("invalid row was silently dropped", err)
	}
	if _, err := worker.DispatchOne(context.Background()); err != nil {
		t.Fatal("poison row blocked following signup", err)
	}
	if published, attempts, _ := authOutboxState(t, db, invalid.ID); published || attempts != 1 || calls != 1 {
		t.Fatal("invalid row was not retained")
	}
}

func TestAuthOutboxCancellationLeavesWorkRecoverable(t *testing.T) {
	db := hardeningDatabase(t)
	e := seedAuthOutbox(t, db)
	entered := make(chan struct{})
	worker := outbox.NewWorker(db, authPublisherFunc(func(ctx context.Context, _ outbox.Message) error { close(entered); <-ctx.Done(); return ctx.Err() }), 50*time.Millisecond, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx, nil); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown was unbounded")
	}
	if published, attempts, delay := authOutboxState(t, db, e.ID); published || attempts != 1 || delay < 0 {
		t.Fatal("cancelled delivery was lost")
	}
}
