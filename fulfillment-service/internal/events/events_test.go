package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOrderReadyEnvelope(t *testing.T) {
	d := OrderReadyData{OrderID: uuid.New(), QueueNumber: "01", PickupSlotID: uuid.New()}
	env, err := NewOrderReady(time.Date(2026, 9, 2, 14, 55, 0, 0, time.UTC), d)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(env)

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"specversion": "1.0", "source": Source, "type": TypeOrderReady,
		"datacontenttype": "application/json", "time": "2026-09-02T14:55:00Z",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %s", k, m[k], v)
		}
	}
	if _, ok := m["traceparent"]; ok {
		t.Error("traceparent must be omitted when there is no inbound trace")
	}
	data := m["data"].(map[string]any)
	if data["queue_number"] != "01" || data["order_id"] != d.OrderID.String() {
		t.Errorf("data = %v", data)
	}

	if _, err := Parse(body); err != nil {
		t.Errorf("Parse(own envelope): %v", err)
	}
}

func TestOrderQueuedEnvelope(t *testing.T) {
	d := OrderQueuedData{
		OrderID: uuid.New(), CustomerID: uuid.New(), QueueNumber: "03",
		PickupSlotID: uuid.New(), PickupAt: time.Date(2026, 9, 2, 15, 0, 0, 0, time.FixedZone("ICT", 7*3600)),
	}
	env, err := NewOrderQueued(time.Date(2026, 9, 2, 14, 31, 0, 0, time.UTC), d)
	if err != nil {
		t.Fatal(err)
	}
	if RoutingKeyOrderQueued != "fulfillment.order_queued" {
		t.Errorf("routing key = %s", RoutingKeyOrderQueued)
	}
	body, _ := json.Marshal(env)

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "com.gelatoflow.fulfillment.order-queued.v1" || m["source"] != "/gelatoflow/fulfillment-service" {
		t.Errorf("envelope = %v", m)
	}
	data := m["data"].(map[string]any)
	want := map[string]string{
		"order_id": d.OrderID.String(), "customer_id": d.CustomerID.String(), "queue_number": "03",
		"pickup_slot_id": d.PickupSlotID.String(), "pickup_at": "2026-09-02T08:00:00Z",
	}
	if len(data) != len(want) {
		t.Errorf("data has %d fields, want exactly %d: %v", len(data), len(want), data)
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data.%s = %v, want %s", k, data[k], v)
		}
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"bad json":      `{`,
		"wrong version": `{"specversion":"0.3","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"t","data":{}}`,
		"missing id":    `{"specversion":"1.0","type":"t","data":{}}`,
		"missing data":  `{"specversion":"1.0","id":"c3f8510a-6c17-48b2-b1ef-24901b0f5556","type":"t"}`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
