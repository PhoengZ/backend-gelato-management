package tests

import (
	invpb "batch-inventory-service/gen/inventory/v1"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/migrations"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func confirmed(t *testing.T, s *inventory.Service, items ...inventory.Item) inventory.Reservation {
	t.Helper()
	r := reserve(t, s, items...)
	c, err := s.Transition(context.Background(), true, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()})
	if err != nil || c.Status != "CONFIRMED" {
		t.Fatal(err, c)
	}
	return c
}

func movementCount(t *testing.T, s *inventory.Service, reservationID, kind string) (int64, int64) {
	t.Helper()
	var count, total int64
	if err := s.DB.QueryRow(context.Background(), "SELECT count(*),COALESCE(sum(portions),0) FROM inventory_movements WHERE reservation_id=$1 AND kind=$2", reservationID, kind).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	return count, total
}

func TestReturnConfirmedReservationRestoresSoldPortionsOnce(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	flavor := uuid.NewString()
	early := batch(t, s, flavor, 2, time.Now().Add(time.Hour))
	late := batch(t, s, flavor, 4, time.Now().Add(2*time.Hour))
	r := confirmed(t, s, inventory.Item{FlavorID: flavor, Portions: 3})
	assertBalance(t, s, early.ID, 0, 0, 2, 0)
	assertBalance(t, s, late.ID, 3, 0, 1, 0)

	in := inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString(), Reason: "STORE_CANCELLED"}
	got, err := s.Return(ctx, in)
	if err != nil || got.Status != "RETURNED" || len(got.Allocations) != 2 {
		t.Fatal(err, got)
	}
	assertBalance(t, s, early.ID, 2, 0, 0, 0)
	assertBalance(t, s, late.ID, 4, 0, 0, 0)
	if n, total := movementCount(t, s, r.ID, "RETURNED"); n != 2 || total != 3 {
		t.Fatalf("returned movements %d/%d", n, total)
	}

	replay, err := s.Return(ctx, in)
	if err != nil || replay.Status != "RETURNED" || replay.ID != r.ID {
		t.Fatal("same-key replay", err, replay)
	}
	in.Key = uuid.NewString()
	again, err := s.Return(ctx, in)
	if err != nil || again.Status != "RETURNED" {
		t.Fatal("already returned must be a no-op", err, again)
	}
	assertBalance(t, s, early.ID, 2, 0, 0, 0)
	assertBalance(t, s, late.ID, 4, 0, 0, 0)
	if n, _ := movementCount(t, s, r.ID, "RETURNED"); n != 2 {
		t.Fatalf("stock returned twice: %d movements", n)
	}
	if _, err = s.Return(ctx, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: in.Key, Reason: "OTHER"}); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("key reused with a different request", err)
	}
	if _, err = s.Transition(ctx, false, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()}); !errors.Is(err, inventory.ErrState) {
		t.Fatal("released a returned reservation", err)
	}
	if _, err = s.Transition(ctx, true, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()}); !errors.Is(err, inventory.ErrState) {
		t.Fatal("confirmed a returned reservation", err)
	}
}

func TestReturnConfirmedReservationWastesExpiredBatches(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 5, time.Now().Add(time.Hour))
	r := confirmed(t, s, inventory.Item{FlavorID: flavor, Portions: 2})
	if _, err := s.DB.Exec(ctx, "UPDATE batches SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", b.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Return(ctx, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString(), Reason: "STORE_CANCELLED"})
	if err != nil || got.Status != "RETURNED" {
		t.Fatal(err, got)
	}
	assertBalance(t, s, b.ID, 3, 0, 0, 2)
	if n, total := movementCount(t, s, r.ID, "WASTED"); n != 1 || total != 2 {
		t.Fatalf("wasted movements %d/%d", n, total)
	}
	var events int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE payload->>'type' = 'com.gelatoflow.inventory.waste-recorded.v1'").Scan(&events); err != nil || events != 1 {
		t.Fatal(events, err)
	}
}

func TestReturnRejectsUnconfirmedAndForeignReservations(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 5, time.Now().Add(time.Hour))
	active := reserve(t, s, inventory.Item{FlavorID: flavor, Portions: 1})
	if _, err := s.Return(ctx, inventory.TransitionInput{ReservationID: active.ID, OrderID: active.OrderID, Key: uuid.NewString()}); !errors.Is(err, inventory.ErrState) {
		t.Fatal("returned an ACTIVE reservation", err)
	}
	c := confirmed(t, s, inventory.Item{FlavorID: flavor, Portions: 1})
	if _, err := s.Return(ctx, inventory.TransitionInput{ReservationID: c.ID, OrderID: uuid.NewString(), Key: uuid.NewString()}); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal("order mismatch", err)
	}
	if _, err := s.Return(ctx, inventory.TransitionInput{ReservationID: "bad", OrderID: c.OrderID, Key: uuid.NewString()}); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal("invalid id", err)
	}
	assertBalance(t, s, b.ID, 3, 1, 1, 0)
}

func TestConcurrentReturnsReturnStockOnce(t *testing.T) {
	s, _ := fixture(t)
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 5, time.Now().Add(time.Hour))
	r := confirmed(t, s, inventory.Item{FlavorID: flavor, Portions: 2})
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := s.Return(context.Background(), inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString(), Reason: "STORE_CANCELLED"})
			errs <- err
		}()
	}
	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	assertBalance(t, s, b.ID, 5, 0, 0, 0)
}

func TestReturnMigrationUpgradesExistingSchemaWithoutDataLoss(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 4, time.Now().Add(time.Hour))
	r := confirmed(t, s, inventory.Item{FlavorID: flavor, Portions: 1})
	// Recreate the pre-007 production schema: narrower checks and no marker.
	if _, err := s.DB.Exec(ctx, `ALTER TABLE reservations DROP CONSTRAINT reservations_status_check;
		ALTER TABLE reservations ADD CONSTRAINT reservations_status_check CHECK (status IN ('ACTIVE','CONFIRMED','RELEASED','EXPIRED'));
		ALTER TABLE inventory_movements DROP CONSTRAINT inventory_movements_kind_check;
		ALTER TABLE inventory_movements ADD CONSTRAINT inventory_movements_kind_check CHECK (kind IN ('PRODUCED','RESERVED','CONFIRMED','RELEASED','WASTED'));
		DELETE FROM inventory_schema_migrations WHERE version='007_reservation_return.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.CheckReady(ctx, s.DB); err == nil {
		t.Fatal("readiness accepted a schema without the return migration")
	}
	if _, err := s.Return(ctx, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()}); err == nil {
		t.Fatal("legacy schema accepted RETURNED")
	}
	assertBalance(t, s, b.ID, 3, 0, 1, 0)
	if err := migrations.Apply(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	if err := migrations.CheckReady(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Return(ctx, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()}); err != nil || got.Status != "RETURNED" {
		t.Fatal(err, got)
	}
	assertBalance(t, s, b.ID, 4, 0, 0, 0)
}

func TestReturnConfirmedReservationGRPC(t *testing.T) {
	s, _ := fixture(t)
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 3, time.Now().Add(time.Hour))
	client := rpcClient(t, s)
	ctx := rpcCtx(t)
	reserved, err := client.ReservePortions(ctx, &invpb.ReservePortionsRequest{OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString(), Items: []*invpb.PortionRequest{{FlavorId: flavor, Portions: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	res := reserved.GetReservation()
	req := &invpb.ReturnConfirmedReservationRequest{ReservationId: res.GetReservationId(), OrderId: res.GetOrderId(), IdempotencyKey: uuid.NewString(), Reason: "STORE_CANCELLED"}
	if _, err = client.ReturnConfirmedReservation(ctx, req); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("returned unconfirmed reservation", err)
	}
	if _, err = client.ConfirmReservation(ctx, &invpb.ConfirmReservationRequest{ReservationId: res.GetReservationId(), OrderId: res.GetOrderId(), IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	req.IdempotencyKey = uuid.NewString()
	out, err := client.ReturnConfirmedReservation(ctx, req)
	if err != nil || out.GetReservation().GetStatus() != invpb.ReservationStatus_RESERVATION_STATUS_RETURNED {
		t.Fatal(err, out)
	}
	assertBalance(t, s, b.ID, 3, 0, 0, 0)
}
