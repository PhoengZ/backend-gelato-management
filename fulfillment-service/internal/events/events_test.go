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
