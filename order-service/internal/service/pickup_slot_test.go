package service

import (
	"context"
	"errors"
	"testing"
	"time"

	paymentv1 "order-service/gen/payment/v1"

	"github.com/google/uuid"
)

type mockFulfillmentClient struct {
	capacity int
	err      error
	calls    int
}

func (m *mockFulfillmentClient) CheckSlotCapacity(_ context.Context, _ time.Time) (int, error) {
	m.calls++
	return m.capacity, m.err
}

func TestPickupSlotAlignment(t *testing.T) {
	r := &checkoutRepo{}
	i := &checkoutInventory{}
	p := &checkoutPayment{call: func(req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		return sessionResponse(), nil
	}}
	s, err := NewOrderService(r, i, &checkoutCatalog{}, p)
	if err != nil {
		t.Fatalf("NewOrderService failed: %v", err)
	}

	baseTime := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Hour)

	t.Run("valid 15-minute aligned boundaries", func(t *testing.T) {
		for _, minute := range []int{0, 15, 30, 45} {
			slot := baseTime.Add(time.Duration(minute) * time.Minute)
			input := checkoutInput()
			input.PickupAt = slot
			_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
			if err != nil {
				t.Errorf("expected success for %v, got error: %v", slot, err)
			}
		}
	})

	t.Run("unaligned minutes returns 400", func(t *testing.T) {
		for _, minute := range []int{1, 7, 14, 16, 29, 31, 59} {
			slot := baseTime.Add(time.Duration(minute) * time.Minute)
			input := checkoutInput()
			input.PickupAt = slot
			_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
			if err == nil {
				t.Fatalf("expected error for unaligned minute %d, got nil", minute)
			}
			appErr, ok := err.(*AppError)
			if !ok || appErr.HTTPStatus != 400 || appErr.Code != "INVALID_ARGUMENT" {
				t.Errorf("expected 400 INVALID_ARGUMENT, got: %v", err)
			}
		}
	})

	t.Run("unaligned seconds returns 400", func(t *testing.T) {
		slot := baseTime.Add(15*time.Minute + 10*time.Second)
		input := checkoutInput()
		input.PickupAt = slot
		_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
		if err == nil {
			t.Fatal("expected error for unaligned seconds, got nil")
		}
		appErr, ok := err.(*AppError)
		if !ok || appErr.HTTPStatus != 400 || appErr.Code != "INVALID_ARGUMENT" {
			t.Errorf("expected 400 INVALID_ARGUMENT, got: %v", err)
		}
	})
}

func TestPickupSlotCapacity(t *testing.T) {
	r := &checkoutRepo{}
	i := &checkoutInventory{}
	p := &checkoutPayment{call: func(req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		return sessionResponse(), nil
	}}

	alignedSlot := time.Now().UTC().Add(2 * time.Hour).Truncate(15 * time.Minute)

	t.Run("capacity below default limit 10 succeeds", func(t *testing.T) {
		mc := &mockFulfillmentClient{capacity: 9}
		s, err := NewOrderService(r, i, &checkoutCatalog{}, p, WithFulfillmentClient(mc))
		if err != nil {
			t.Fatalf("NewOrderService failed: %v", err)
		}

		input := checkoutInput()
		input.PickupAt = alignedSlot
		_, err = s.PlaceOrder(context.Background(), input, uuid.NewString())
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if mc.calls != 1 {
			t.Errorf("expected 1 call to fulfillment, got %d", mc.calls)
		}
	})

	t.Run("capacity equal to limit 10 returns 409 SLOT_CAPACITY_EXCEEDED", func(t *testing.T) {
		mc := &mockFulfillmentClient{capacity: 10}
		s, _ := NewOrderService(r, i, &checkoutCatalog{}, p, WithFulfillmentClient(mc))

		input := checkoutInput()
		input.PickupAt = alignedSlot
		_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		appErr, ok := err.(*AppError)
		if !ok || appErr.HTTPStatus != 409 || appErr.Code != "SLOT_CAPACITY_EXCEEDED" {
			t.Fatalf("expected 409 SLOT_CAPACITY_EXCEEDED, got: %v", err)
		}
	})

	t.Run("capacity exceeding limit returns 409 SLOT_CAPACITY_EXCEEDED", func(t *testing.T) {
		mc := &mockFulfillmentClient{capacity: 15}
		s, _ := NewOrderService(r, i, &checkoutCatalog{}, p, WithFulfillmentClient(mc))

		input := checkoutInput()
		input.PickupAt = alignedSlot
		_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		appErr, ok := err.(*AppError)
		if !ok || appErr.HTTPStatus != 409 || appErr.Code != "SLOT_CAPACITY_EXCEEDED" {
			t.Fatalf("expected 409 SLOT_CAPACITY_EXCEEDED, got: %v", err)
		}
	})

	t.Run("custom max capacity option is enforced", func(t *testing.T) {
		mc := &mockFulfillmentClient{capacity: 3}
		s, _ := NewOrderService(r, i, &checkoutCatalog{}, p,
			WithFulfillmentClient(mc),
			WithMaxSlotCapacity(3),
		)

		input := checkoutInput()
		input.PickupAt = alignedSlot
		_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		appErr, ok := err.(*AppError)
		if !ok || appErr.HTTPStatus != 409 || appErr.Code != "SLOT_CAPACITY_EXCEEDED" {
			t.Fatalf("expected 409 SLOT_CAPACITY_EXCEEDED, got: %v", err)
		}
	})

	t.Run("fulfillment error returns 503 FULFILLMENT_UNAVAILABLE", func(t *testing.T) {
		mc := &mockFulfillmentClient{err: errors.New("network timeout")}
		s, _ := NewOrderService(r, i, &checkoutCatalog{}, p, WithFulfillmentClient(mc))

		input := checkoutInput()
		input.PickupAt = alignedSlot
		_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		appErr, ok := err.(*AppError)
		if !ok || appErr.HTTPStatus != 503 || appErr.Code != "FULFILLMENT_UNAVAILABLE" {
			t.Fatalf("expected 503 FULFILLMENT_UNAVAILABLE, got: %v", err)
		}
	})

	t.Run("idempotent replay does not re-query fulfillment capacity", func(t *testing.T) {
		mc := &mockFulfillmentClient{capacity: 2}
		s, _ := NewOrderService(r, i, &checkoutCatalog{}, p, WithFulfillmentClient(mc))

		input := checkoutInput()
		input.PickupAt = alignedSlot
		idemKey := uuid.NewString()

		res1, err := s.PlaceOrder(context.Background(), input, idemKey)
		if err != nil {
			t.Fatalf("first placeOrder failed: %v", err)
		}
		if mc.calls != 1 {
			t.Fatalf("expected 1 call, got %d", mc.calls)
		}

		res2, err := s.PlaceOrder(context.Background(), input, idemKey)
		if err != nil {
			t.Fatalf("second placeOrder failed: %v", err)
		}
		if res1.ID != res2.ID {
			t.Errorf("got different order IDs %s != %s", res1.ID, res2.ID)
		}
		if mc.calls != 1 {
			t.Errorf("idempotent retry called fulfillment again: calls=%d", mc.calls)
		}
	})
}
