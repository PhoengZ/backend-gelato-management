package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func bangkok(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func limitedService(t *testing.T, opts ...Option) Service {
	t.Helper()
	s, err := NewOrderService(&checkoutRepo{}, &checkoutInventory{}, &checkoutCatalog{}, &checkoutPayment{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertBadRequestCode(t *testing.T, err error, code string) {
	t.Helper()
	appErr, ok := err.(*AppError)
	if !ok || appErr.HTTPStatus != 400 || appErr.Code != code {
		t.Fatalf("error = %#v, want 400 %s", err, code)
	}
}

func TestPickupOnAnotherDayIsRejected(t *testing.T) {
	s := limitedService(t, WithSameDayPickup(bangkok(t)))
	input := checkoutInput()
	input.PickupAt = time.Now().UTC().Add(48 * time.Hour).Truncate(15 * time.Minute)
	_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
	assertBadRequestCode(t, err, "PICKUP_NOT_SAME_DAY")
}

func TestSameCalendarDayUsesTheShopTimezone(t *testing.T) {
	loc := bangkok(t)
	now := time.Date(2026, 11, 7, 23, 30, 0, 0, loc)
	for _, tc := range []struct {
		pickup time.Time
		want   bool
	}{
		{time.Date(2026, 11, 7, 23, 45, 0, 0, loc), true},
		{time.Date(2026, 11, 8, 0, 0, 0, 0, loc), false},
		// 18:00 UTC is already the next morning in Bangkok.
		{time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC), false},
	} {
		if got := sameCalendarDay(now.UTC(), tc.pickup, loc); got != tc.want {
			t.Errorf("sameCalendarDay(%v) = %v, want %v", tc.pickup, got, tc.want)
		}
	}
}

func TestPortionsAboveTheOrderLimitAreRejected(t *testing.T) {
	s := limitedService(t, WithMaxPortionsPerOrder(20))
	input := checkoutInput()
	input.Items = []OrderItemInput{
		{FlavorID: uuid.NewString(), Portions: 15},
		{FlavorID: uuid.NewString(), Portions: 6},
	}
	_, err := s.PlaceOrder(context.Background(), input, uuid.NewString())
	assertBadRequestCode(t, err, "PORTIONS_LIMIT_EXCEEDED")
}
