package outbox

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"auth-service/internal/model"
	"github.com/google/uuid"
)

func TestMessagePreservesIdentityWithoutPublishingPrivateFields(t *testing.T) {
	userID := uuid.New()
	event := model.OutboxEvent{ID: uuid.New(), EventType: RoutingKey, CreatedAt: time.Now(), Payload: json.RawMessage(`{"user_id":"` + userID.String() + `","role":"CUSTOMER","status":"ACTIVE","email":"private@example.invalid","display_name":"Private Name","password":"private-password","unexpected":"private-extra"}`)}
	one, err := MessageFor(event)
	if err != nil {
		t.Fatal(err)
	}
	two, err := MessageFor(event)
	if err != nil || one.ID != event.ID || !one.CreatedAt.Equal(event.CreatedAt) || !bytes.Equal(one.Body, two.Body) {
		t.Fatal("retry changed immutable event facts")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(one.Body, &envelope); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope["data"], &data); err != nil || len(data) != 3 || data["user_id"] != userID.String() {
		t.Fatal("unexpected identity projection")
	}
	for _, secret := range []string{"private@", "Private Name", "private-password", "private-extra", "password", "email", "display_name"} {
		if bytes.Contains(one.Body, []byte(secret)) {
			t.Fatalf("published private field %s", secret)
		}
	}
}

func TestMalformedOutboxFactsAreNotPublished(t *testing.T) {
	valid := model.OutboxEvent{ID: uuid.New(), EventType: RoutingKey, CreatedAt: time.Now(), Payload: json.RawMessage(`{"user_id":"` + uuid.NewString() + `","role":"CUSTOMER","status":"ACTIVE"}`)}
	for _, name := range []string{"nil_event_id", "unknown_type", "missing_time", "malformed_json", "nil_user", "bad_role", "bad_status", "oversized"} {
		t.Run(name, func(t *testing.T) {
			e := valid
			switch name {
			case "nil_event_id":
				e.ID = uuid.Nil
			case "unknown_type":
				e.EventType = "unknown"
			case "missing_time":
				e.CreatedAt = time.Time{}
			case "malformed_json":
				e.Payload = []byte("{")
			case "nil_user":
				e.Payload = []byte(`{"role":"CUSTOMER","status":"ACTIVE"}`)
			case "bad_role":
				e.Payload = bytes.Replace(e.Payload, []byte("CUSTOMER"), []byte("ADMIN"), 1)
			case "bad_status":
				e.Payload = bytes.Replace(e.Payload, []byte("ACTIVE"), []byte("UNKNOWN"), 1)
			case "oversized":
				e.Payload = make([]byte, 65537)
			}
			if _, err := MessageFor(e); err != ErrInvalidEvent {
				t.Fatal("invalid event accepted")
			}
		})
	}
}
