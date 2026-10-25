// Package outbox delivers Auth's committed events independently of HTTP requests.
package outbox

import (
	"encoding/json"
	"errors"
	"time"

	"auth-service/internal/model"
	"github.com/google/uuid"
)

const RoutingKey = "user.registered"
const EventType = "com.gelatoflow.auth.user-registered.v1"

var ErrInvalidEvent = errors.New("invalid Auth outbox event")

type Message struct {
	ID        uuid.UUID
	CreatedAt time.Time
	Body      []byte
}

// MessageFor adapts existing rows without changing their IDs, timestamps or data.
// Only an allowlisted identity projection leaves Auth; old email/name fields stay private.
func MessageFor(event model.OutboxEvent) (Message, error) {
	if event.ID == uuid.Nil || event.EventType != RoutingKey || event.CreatedAt.IsZero() || len(event.Payload) > 64*1024 {
		return Message{}, ErrInvalidEvent
	}
	var data struct {
		UserID uuid.UUID        `json:"user_id"`
		Role   model.Role       `json:"role"`
		Status model.UserStatus `json:"status"`
	}
	if json.Unmarshal(event.Payload, &data) != nil || data.UserID == uuid.Nil || !data.Role.Valid() || (data.Status != model.UserStatusActive && data.Status != model.UserStatusDisabled) {
		return Message{}, ErrInvalidEvent
	}
	body, err := json.Marshal(struct {
		SpecVersion     string    `json:"specversion"`
		ID              uuid.UUID `json:"id"`
		Source          string    `json:"source"`
		Type            string    `json:"type"`
		Subject         string    `json:"subject"`
		Time            time.Time `json:"time"`
		DataContentType string    `json:"datacontenttype"`
		Data            any       `json:"data"`
	}{"1.0", event.ID, "urn:gelatoflow:auth-service", EventType, "users/" + data.UserID.String(), event.CreatedAt.UTC(), "application/json", data})
	if err != nil {
		return Message{}, ErrInvalidEvent
	}
	return Message{ID: event.ID, CreatedAt: event.CreatedAt.UTC(), Body: body}, nil
}
