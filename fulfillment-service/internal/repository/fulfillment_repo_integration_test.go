//go:build integration

// Run against a disposable PostgreSQL database:
//
//	FULFILLMENT_TEST_DATABASE_URL=postgres://... go test -tags integration ./internal/repository/...
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fulfillment-service/internal/events"
	"fulfillment-service/internal/models"
	"fulfillment-service/migrations"
)

func newTestRepo(t *testing.T) (*FulfillmentRepository, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("FULFILLMENT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("FULFILLMENT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	// TRUNCATE does not fire the row-level trigger that blocks history deletes.
	if _, err := pool.Exec(ctx, `TRUNCATE fulfillment_status_history, fulfillments, pickup_slots,
		processed_events, outbox, cancelled_orders CASCADE`); err != nil {
		t.Fatal(err)
	}
	return NewFulfillmentRepository(pool), pool
}

func placedData(t *testing.T, orderID, customerID uuid.UUID, pickupAt time.Time) events.OrderPlacedData {
	t.Helper()
	var d events.OrderPlacedData
	raw := fmt.Sprintf(`{"order_id":%q,"customer_id":%q,"pickup_at":%q,
		"items":[{"flavor_id":"0f3bca11-1eb2-4a86-908a-e60d7656c79b","flavor_name":"Pistachio","portions":1}]}`,
		orderID, customerID, pickupAt.Format(time.RFC3339))
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIntegrationPlacedStoresCustomerAndSourceEvent(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()
	orderID, customerID, eventID := uuid.New(), uuid.New(), uuid.New()

	created, err := repo.CreateFromOrderPlaced(ctx, eventID, placedData(t, orderID, customerID, time.Date(2026, 10, 13, 14, 5, 0, 0, time.UTC)))
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if again, err := repo.CreateFromOrderPlaced(ctx, eventID, placedData(t, orderID, customerID, time.Now())); err != nil || again {
		t.Fatalf("duplicate event: created=%v err=%v", again, err)
	}

	f, err := repo.GetByOrderID(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if f.CustomerID == nil || *f.CustomerID != customerID || f.QueueNumber != "01" {
		t.Fatalf("ticket = %+v", f)
	}

	cancelEvent := uuid.New()
	if ok, err := repo.CancelByOrder(ctx, cancelEvent, orderID); err != nil || !ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	history, err := repo.GetHistory(ctx, f.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %+v err=%v", history, err)
	}
	if history[0].SourceEventID == nil || *history[0].SourceEventID != eventID {
		t.Errorf("first entry source = %v, want %s", history[0].SourceEventID, eventID)
	}
	if history[1].SourceEventID == nil || *history[1].SourceEventID != cancelEvent {
		t.Errorf("cancel entry source = %v, want %s", history[1].SourceEventID, cancelEvent)
	}
}

func TestIntegrationCancelBeforePlacedLeavesNoTicket(t *testing.T) {
	repo, pool := newTestRepo(t)
	ctx := context.Background()
	orderID := uuid.New()

	if ok, err := repo.CancelByOrder(ctx, uuid.New(), orderID); err != nil || ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	created, err := repo.CreateFromOrderPlaced(ctx, uuid.New(), placedData(t, orderID, uuid.New(), time.Now().Add(time.Hour)))
	if err != nil || created {
		t.Fatalf("late order.placed: created=%v err=%v", created, err)
	}
	if _, err := repo.GetByOrderID(ctx, orderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByOrderID err = %v, want ErrNotFound", err)
	}
	var slots int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pickup_slots`).Scan(&slots); err != nil || slots != 0 {
		t.Fatalf("pickup slots = %d err=%v; a cancelled order must not take a queue number", slots, err)
	}
}

func TestIntegrationListFilterAndOrder(t *testing.T) {
	repo, _ := newTestRepo(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

	late := uuid.New()
	early1, early2 := uuid.New(), uuid.New()
	nextDay := uuid.New()
	for _, p := range []struct {
		order uuid.UUID
		at    time.Time
	}{
		{late, day.Add(15 * time.Hour)},
		{early1, day.Add(14 * time.Hour)},
		{early2, day.Add(14*time.Hour + 5*time.Minute)},
		{nextDay, day.Add(38 * time.Hour)},
	} {
		if _, err := repo.CreateFromOrderPlaced(ctx, uuid.New(), placedData(t, p.order, uuid.New(), p.at)); err != nil {
			t.Fatal(err)
		}
	}

	from, to := day, day.Add(24*time.Hour)
	items, err := repo.List(ctx, models.ListFilter{
		Statuses:   []models.Status{models.StatusPreparing},
		PickupFrom: &from,
		PickupTo:   &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []uuid.UUID
	for _, f := range items {
		got = append(got, f.OrderID)
	}
	want := []uuid.UUID{early1, early2, late}
	if len(got) != len(want) {
		t.Fatalf("got %d tickets, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %s, want %s (slot then queue order)", i, got[i], want[i])
		}
	}

	all, err := repo.List(ctx, models.ListFilter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("unfiltered list = %d err=%v, want 4", len(all), err)
	}
}

func TestIntegrationPurgeExpired(t *testing.T) {
	repo, pool := newTestRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)

	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO outbox (id, routing_key, payload, published_at) VALUES ($1, 'k', '{}', $2)`, []any{uuid.New(), old}},
		{`INSERT INTO outbox (id, routing_key, payload, published_at) VALUES ($1, 'k', '{}', NULL)`, []any{uuid.New()}},
		{`INSERT INTO outbox (id, routing_key, payload, published_at) VALUES ($1, 'k', '{}', $2)`, []any{uuid.New(), now}},
		{`INSERT INTO processed_events (event_id, processed_at) VALUES ($1, $2)`, []any{uuid.New(), old}},
		{`INSERT INTO processed_events (event_id, processed_at) VALUES ($1, $2)`, []any{uuid.New(), now}},
		{`INSERT INTO cancelled_orders (order_id, event_id, cancelled_at) VALUES ($1, $2, $3)`, []any{uuid.New(), uuid.New(), old}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	n, err := repo.PurgeExpired(ctx, now)
	if err != nil || n != 3 {
		t.Fatalf("purged %d err=%v, want 3", n, err)
	}
	var outbox, processed int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&outbox)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&processed)
	if outbox != 2 || processed != 1 {
		t.Fatalf("left outbox=%d processed=%d, want unpublished+recent outbox and the recent event", outbox, processed)
	}
}
