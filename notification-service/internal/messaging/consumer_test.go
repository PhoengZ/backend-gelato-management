package messaging

import (
	"os"
	"testing"

	"notification-service/internal/events"
)

type recorder struct {
	placed    []events.OrderPlacedData
	ready     []events.OrderReadyData
	cancelled []events.OrderCancelledData
	lowStock  []events.BatchLowStockData
	expiring  []events.BatchExpiringData
}

func (r *recorder) OrderPlaced(_ events.Envelope, d events.OrderPlacedData) {
	r.placed = append(r.placed, d)
}
func (r *recorder) OrderReady(_ events.Envelope, d events.OrderReadyData) {
	r.ready = append(r.ready, d)
}
func (r *recorder) OrderCancelled(_ events.Envelope, d events.OrderCancelledData) {
	r.cancelled = append(r.cancelled, d)
}
func (r *recorder) BatchLowStock(_ events.Envelope, d events.BatchLowStockData) {
	r.lowStock = append(r.lowStock, d)
}
func (r *recorder) BatchExpiring(_ events.Envelope, d events.BatchExpiringData) {
	r.expiring = append(r.expiring, d)
}

func envelope(eventType, data string) []byte {
	return []byte(`{"specversion":"1.0","id":"5b0c1f1e-3a52-4d8c-9a41-0d6f4f0f7a10","source":"/x","type":"` +
		eventType + `","time":"2026-09-02T14:55:00Z","datacontenttype":"application/json","data":` + data + `}`)
}

// contractExample loads a fixture from contracts/examples so the tests fail if
// the consumer drifts from the published event shape.
func contractExample(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("../../../contracts/examples/" + name)
	if err != nil {
		t.Fatalf("read contract example: %v", err)
	}
	return body
}

func TestDispatchOrderPlacedContractExample(t *testing.T) {
	r := &recorder{}
	retry, err := Dispatch(r, contractExample(t, "order-placed.v1.json"))
	if err != nil || retry {
		t.Fatalf("Dispatch() = retry %v, err %v", retry, err)
	}
	if len(r.placed) != 1 {
		t.Fatalf("placed events = %d, want 1", len(r.placed))
	}
	got := r.placed[0]
	if got.CustomerID.String() != "8dc81c31-a354-4774-b835-36ea0411bc54" || got.TotalAmountMinor != 1250 || len(got.Items) != 1 {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestDispatchOrderReadyContractExample(t *testing.T) {
	r := &recorder{}
	if retry, err := Dispatch(r, contractExample(t, "fulfillment-order-ready.v1.json")); err != nil || retry {
		t.Fatalf("Dispatch() = retry %v, err %v", retry, err)
	}
	if len(r.ready) != 1 || r.ready[0].QueueNumber != "01" {
		t.Errorf("ready = %+v", r.ready)
	}
}

func TestDispatchOrderCancelledContractExample(t *testing.T) {
	r := &recorder{}
	if retry, err := Dispatch(r, contractExample(t, "order-cancelled.v1.json")); err != nil || retry {
		t.Fatalf("Dispatch() = retry %v, err %v", retry, err)
	}
	if len(r.cancelled) != 1 || r.cancelled[0].Reason != "CUSTOMER_REQUEST" {
		t.Errorf("cancelled = %+v", r.cancelled)
	}
}

func TestDispatchOtherEvents(t *testing.T) {
	r := &recorder{}
	orderID := "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594"
	batchID := "11111111-1111-4111-8111-111111111111"
	flavorID := "22222222-2222-4222-8222-222222222222"

	bodies := [][]byte{
		envelope(events.TypeOrderCancelled, `{"order_id":"`+orderID+`","reason":"PAYMENT_EXPIRED","extra":true}`),
		envelope(events.TypeBatchLowStock, `{"batch_id":"`+batchID+`","flavor_id":"`+flavorID+`","remaining_portions":3,"threshold_portions":5}`),
		envelope(events.TypeBatchExpiring, `{"batch_id":"`+batchID+`","flavor_id":"`+flavorID+`","expires_at":"2026-09-03T00:00:00Z"}`),
	}
	for _, b := range bodies {
		if retry, err := Dispatch(r, b); err != nil || retry {
			t.Fatalf("Dispatch() = retry %v, err %v", retry, err)
		}
	}
	if len(r.cancelled) != 1 || r.cancelled[0].Reason != "PAYMENT_EXPIRED" {
		t.Errorf("cancelled = %+v", r.cancelled)
	}
	if len(r.lowStock) != 1 || r.lowStock[0].RemainingPortions != 3 {
		t.Errorf("lowStock = %+v", r.lowStock)
	}
	if len(r.expiring) != 1 || r.expiring[0].ExpiresAt.IsZero() {
		t.Errorf("expiring = %+v", r.expiring)
	}
}

func TestDispatchIgnoresUnknownType(t *testing.T) {
	r := &recorder{}
	retry, err := Dispatch(r, envelope("com.gelatoflow.something.else.v1", `{}`))
	if err != nil || retry {
		t.Fatalf("unknown type should be ignored, got retry %v err %v", retry, err)
	}
}

func TestDispatchDropsMalformedMessages(t *testing.T) {
	tests := map[string][]byte{
		"not json":           []byte(`nope`),
		"wrong specversion":  []byte(`{"specversion":"0.3","id":"5b0c1f1e-3a52-4d8c-9a41-0d6f4f0f7a10","type":"t","data":{}}`),
		"missing id":         []byte(`{"specversion":"1.0","type":"t","data":{}}`),
		"placed no customer": envelope(events.TypeOrderPlaced, `{"order_id":"8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594"}`),
		"ready no order":     envelope(events.TypeOrderReady, `{"queue_number":"01"}`),
		"cancelled no order": envelope(events.TypeOrderCancelled, `{"reason":"x"}`),
		"bad payload type":   envelope(events.TypeOrderReady, `{"order_id":42}`),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			r := &recorder{}
			retry, err := Dispatch(r, body)
			if err == nil {
				t.Fatal("expected an error")
			}
			if retry {
				t.Error("malformed messages must not be retried")
			}
			if len(r.placed)+len(r.ready)+len(r.cancelled) != 0 {
				t.Error("malformed message must not reach the handler")
			}
		})
	}
}
