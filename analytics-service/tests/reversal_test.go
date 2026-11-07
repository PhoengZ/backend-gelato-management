package tests

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"analytics-service/internal/models"
	"analytics-service/internal/service"
)

const placedJSON = `{
  "specversion": "1.0", "id": "c3f8510a-6c17-48b2-b1ef-24901b0f5556",
  "source": "/gelatoflow/order-service", "type": "com.gelatoflow.order.placed.v1",
  "time": "2026-09-02T14:30:00Z", "datacontenttype": "application/json",
  "data": {"order_id": "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594", "customer_id": "8dc81c31-a354-4774-b835-36ea0411bc54",
    "status": "PAID", "pickup_at": "2026-09-02T15:00:00Z", "total_amount_minor": 1250, "currency": "THB",
    "items": [{"flavor_id": "0f3bca11-1eb2-4a86-908a-e60d7656c79b", "flavor_name": "Sicilian Pistachio", "portions": 2, "unit_price_minor": 625, "subtotal_minor": 1250}]}
}`

// The cancellation is published on a later day; it must reverse the paid_at bucket.
const paidCancelledJSON = `{
  "specversion": "1.0", "id": "e6a1d0b4-7c52-4f0e-9d3a-2b8c5f1a9e47",
  "source": "/gelatoflow/order-service", "type": "com.gelatoflow.order.cancelled.v1",
  "time": "2026-09-03T09:00:00Z", "datacontenttype": "application/json",
  "data": {"order_id": "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594", "reason": "STORE_CANCELLED",
    "customer_id": "8dc81c31-a354-4774-b835-36ea0411bc54", "was_paid": true, "total_amount_minor": 1250, "currency": "THB",
    "items": [{"flavor_id": "0f3bca11-1eb2-4a86-908a-e60d7656c79b", "flavor_name": "Sicilian Pistachio", "portions": 2, "unit_price_minor": 625, "subtotal_minor": 1250}],
    "paid_at": "2026-09-02T14:30:00Z"}
}`

func decodeEvent[T any](t *testing.T, raw string) T {
	t.Helper()
	var event T
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func baselineDay() *models.Analytics {
	return &models.Analytics{
		Date:       "2026-09-02",
		Financials: models.Financials{GrossSalesMinor: 3000, TotalOrders: 2, AverageOrderValueMinor: 1500, Currency: "THB"},
		Operations: models.Operations{ScoopsSold: 6, WasteRate: 0.5},
		WasteStats: models.WasteStats{TotalWastePortions: 3, WasteByReason: []models.WasteByReason{}},
		FlavorStats: []models.FlavorStat{
			{FlavorID: "0f3bca11-1eb2-4a86-908a-e60d7656c79b", Name: "Sicilian Pistachio", ScoopsSold: 4, RevenueMinor: 2000, WastePortions: 1},
			{FlavorID: "11111111-1eb2-4a86-908a-e60d7656c79b", Name: "Vanilla", ScoopsSold: 2, RevenueMinor: 1000},
		},
	}
}

func copyDay(record *models.Analytics) models.Analytics {
	out := *record
	out.FlavorStats = append([]models.FlavorStat(nil), record.FlavorStats...)
	out.ReversedOrderIDs = nil
	return out
}

func TestPaidCancellationReversesExactlyWhatOrderPlacedAdded(t *testing.T) {
	repo := NewMockRepository()
	events := NewMockEventRepository()
	svc := service.NewAnalyticsService(repo, events, NewMockOrderClient())
	repo.Records["2026-09-02"] = baselineDay()
	before := copyDay(repo.Records["2026-09-02"])

	if err := svc.ProcessOrderPlaced(context.Background(), decodeEvent[models.OrderPlacedEvent](t, placedJSON)); err != nil {
		t.Fatal(err)
	}
	if got := repo.Get("2026-09-02"); got.Financials.GrossSalesMinor != 4250 || got.Financials.TotalOrders != 3 {
		t.Fatalf("placed not counted: %+v", got.Financials)
	}
	if err := svc.ProcessOrderCancelled(context.Background(), decodeEvent[models.OrderCancelledEvent](t, paidCancelledJSON)); err != nil {
		t.Fatal(err)
	}
	after := copyDay(repo.Get("2026-09-02"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("reversal is not exact:\nbefore %+v\nafter  %+v", before, after)
	}
	if repo.Get("2026-09-03") != nil {
		t.Fatal("reversal used the cancellation event day instead of paid_at")
	}
}

func TestPaidCancellationRedeliveryDoesNotDoubleCount(t *testing.T) {
	repo := NewMockRepository()
	events := NewMockEventRepository()
	svc := service.NewAnalyticsService(repo, events, NewMockOrderClient())
	repo.Records["2026-09-02"] = baselineDay()
	cancelled := decodeEvent[models.OrderCancelledEvent](t, paidCancelledJSON)
	for i := 0; i < 3; i++ {
		if err := svc.ProcessOrderCancelled(context.Background(), cancelled); err != nil {
			t.Fatal(err)
		}
	}
	// A different event ID for the same order must not subtract again either.
	cancelled.ID = "a0000000-0000-4000-8000-000000000001"
	if err := svc.ProcessOrderCancelled(context.Background(), cancelled); err != nil {
		t.Fatal(err)
	}
	got := repo.Get("2026-09-02")
	if got.Financials.GrossSalesMinor != 1750 || got.Financials.TotalOrders != 1 || got.Operations.ScoopsSold != 4 || got.FlavorStats[0].ScoopsSold != 2 || got.FlavorStats[0].RevenueMinor != 750 {
		t.Fatalf("subtracted more than once: %+v %+v %+v", got.Financials, got.Operations, got.FlavorStats)
	}
	if got.Financials.AverageOrderValueMinor != 1750 {
		t.Fatalf("average order value = %d", got.Financials.AverageOrderValueMinor)
	}
}

// failingMarkRepo loses the processed marker once, as after a crash between the
// projection write and the dedupe write; RabbitMQ then redelivers.
type failingMarkRepo struct {
	*MockEventRepository
	failures int
}

func (r *failingMarkRepo) MarkProcessed(ctx context.Context, id, kind string) error {
	if r.failures > 0 {
		r.failures--
		return errors.New("mongo write failed")
	}
	return r.MockEventRepository.MarkProcessed(ctx, id, kind)
}

func TestPaidCancellationSurvivesLostDedupeMarker(t *testing.T) {
	repo := NewMockRepository()
	events := &failingMarkRepo{MockEventRepository: NewMockEventRepository(), failures: 1}
	svc := service.NewAnalyticsService(repo, events, NewMockOrderClient())
	repo.Records["2026-09-02"] = baselineDay()
	cancelled := decodeEvent[models.OrderCancelledEvent](t, paidCancelledJSON)
	_ = svc.ProcessOrderCancelled(context.Background(), cancelled) // redelivered after this
	if err := svc.ProcessOrderCancelled(context.Background(), cancelled); err != nil {
		t.Fatal(err)
	}
	if got := repo.Get("2026-09-02"); got.Financials.GrossSalesMinor != 1750 || got.Financials.TotalOrders != 1 {
		t.Fatalf("redelivery double counted: %+v", got.Financials)
	}
	if processed, _ := events.IsProcessed(context.Background(), cancelled.ID); !processed {
		t.Fatal("redelivery did not record the event")
	}
}

func TestPaidCancellationWithoutPaidAtIsRejectedPermanently(t *testing.T) {
	repo := NewMockRepository()
	svc := service.NewAnalyticsService(repo, NewMockEventRepository(), NewMockOrderClient())
	cancelled := decodeEvent[models.OrderCancelledEvent](t, paidCancelledJSON)
	cancelled.Data.PaidAt = ""
	if err := svc.ProcessOrderCancelled(context.Background(), cancelled); !errors.Is(err, service.ErrInvalidDate) {
		t.Fatalf("err = %v, want ErrInvalidDate", err)
	}
	if len(repo.Records) != 0 {
		t.Fatal("projection changed")
	}
}

func TestUnpaidCancellationParsesWithoutPaidFacts(t *testing.T) {
	event := decodeEvent[models.OrderCancelledEvent](t, `{"id":"e1","time":"2026-09-02T14:40:00Z","data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","reason":"RESERVATION_EXPIRED"}}`)
	if event.Data.WasPaid || event.Data.OrderID == "" {
		t.Fatalf("decoded %+v", event.Data)
	}
	repo := NewMockRepository()
	repo.Records["2026-09-02"] = baselineDay()
	before := copyDay(repo.Records["2026-09-02"])
	if err := service.NewAnalyticsService(repo, NewMockEventRepository(), NewMockOrderClient()).ProcessOrderCancelled(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if after := copyDay(repo.Get("2026-09-02")); !reflect.DeepEqual(before, after) {
		t.Fatal("unpaid cancellation changed sales")
	}
}
