package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"fulfillment-service/internal/events"
)

type fakeOrders struct {
	placed    []events.OrderPlacedData
	cancelled []uuid.UUID
	err       error
}

func (f *fakeOrders) CreateFromOrderPlaced(_ context.Context, _ uuid.UUID, d events.OrderPlacedData) error {
	f.placed = append(f.placed, d)
	return f.err
}

func (f *fakeOrders) CancelByOrder(_ context.Context, _, orderID uuid.UUID) (bool, error) {
	f.cancelled = append(f.cancelled, orderID)
	return true, f.err
}

const placedBody = `{
  "specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556",
  "source":"/gelatoflow/order-service","type":"com.gelatoflow.order.placed.v1",
  "time":"2026-09-02T14:30:00Z","datacontenttype":"application/json",
  "data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","customer_id":"8dc81c31-a354-4774-b835-36ea0411bc54",
  "status":"PAID","pickup_at":"2026-09-02T15:00:00Z","total_amount_minor":1250,"currency":"THB",
  "items":[{"flavor_id":"0f3bca11-1eb2-4a86-908a-e60d7656c79b","flavor_name":"Sicilian Pistachio","portions":2,
  "unit_price_minor":625,"subtotal_minor":1250}]}}`

const cancelledBody = `{
  "specversion":"1.0","id":"11111111-1111-4111-8111-111111111111",
  "source":"/gelatoflow/order-service","type":"com.gelatoflow.order.cancelled.v1",
  "time":"2026-09-02T14:40:00Z","datacontenttype":"application/json",
  "data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","reason":"CUSTOMER_REQUEST"}}`

func TestDispatchOrderPlaced(t *testing.T) {
	h := &fakeOrders{}
	retry, err := Dispatch(context.Background(), h, []byte(placedBody))
	if err != nil || !retry {
		t.Fatalf("retry=%v err=%v", retry, err)
	}
	if len(h.placed) != 1 || len(h.placed[0].Items) != 1 || h.placed[0].Items[0].Portions != 2 {
		t.Fatalf("placed = %+v", h.placed)
	}
}

func TestDispatchOrderCancelled(t *testing.T) {
	h := &fakeOrders{}
	if _, err := Dispatch(context.Background(), h, []byte(cancelledBody)); err != nil {
		t.Fatal(err)
	}
	if len(h.cancelled) != 1 {
		t.Fatalf("cancelled = %v", h.cancelled)
	}
}

func TestDispatchStorageErrorIsRetried(t *testing.T) {
	h := &fakeOrders{err: errors.New("db down")}
	retry, err := Dispatch(context.Background(), h, []byte(placedBody))
	if err == nil || !retry {
		t.Fatalf("retry=%v err=%v, want retryable error", retry, err)
	}
}

func TestDispatchMalformedIsDropped(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `{`,
		"wrong version": `{"specversion":"2.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"x","data":{}}`,
		"no items":      `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"com.gelatoflow.order.placed.v1","data":{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594","pickup_at":"2026-09-02T15:00:00Z","items":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeOrders{}
			retry, err := Dispatch(context.Background(), h, []byte(body))
			if err == nil || retry {
				t.Fatalf("retry=%v err=%v, want non-retryable error", retry, err)
			}
			if len(h.placed) != 0 {
				t.Error("handler must not be called")
			}
		})
	}
}

func TestDispatchUnknownTypeIgnored(t *testing.T) {
	body := `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"com.gelatoflow.other.v1","data":{}}`
	retry, err := Dispatch(context.Background(), &fakeOrders{}, []byte(body))
	if err != nil || retry {
		t.Fatalf("retry=%v err=%v", retry, err)
	}
}
