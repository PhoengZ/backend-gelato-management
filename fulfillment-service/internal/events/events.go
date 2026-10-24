// Package events holds the CloudEvents 1.0 envelopes Fulfillment publishes and
// consumes. Shapes follow contracts/events/.
package events

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	SpecVersion     = "1.0"
	DataContentType = "application/json"
	Source          = "/gelatoflow/fulfillment-service"

	TypeOrderReady    = "com.gelatoflow.fulfillment.order-ready.v1"
	TypeOrderPickedUp = "com.gelatoflow.fulfillment.order-picked-up.v1"

	TypeOrderPlaced    = "com.gelatoflow.order.placed.v1"
	TypeOrderCancelled = "com.gelatoflow.order.cancelled.v1"

	RoutingKeyOrderReady    = "fulfillment.order_ready"
	RoutingKeyOrderPickedUp = "fulfillment.order_picked_up"
	RoutingKeyOrderPlaced   = "order.placed"
	RoutingKeyOrderCancel   = "order.cancelled"
)

// Envelope is the CloudEvents structured JSON envelope.
type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              uuid.UUID       `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Traceparent     string          `json:"traceparent,omitempty"`
	Data            json.RawMessage `json:"data"`
}

type OrderReadyData struct {
	OrderID      uuid.UUID `json:"order_id"`
	QueueNumber  string    `json:"queue_number"`
	PickupSlotID uuid.UUID `json:"pickup_slot_id"`
}

type OrderPickedUpData struct {
	OrderID    uuid.UUID `json:"order_id"`
	PickedUpAt time.Time `json:"picked_up_at"`
}

// OrderPlacedData is the subset of the order.placed payload Fulfillment needs.
// Unknown fields are ignored so compatible additions do not break the consumer.
type OrderPlacedData struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"` // uuid.Nil when an older producer omits it
	PickupAt   time.Time `json:"pickup_at"`
	Items      []struct {
		FlavorID   uuid.UUID `json:"flavor_id"`
		FlavorName string    `json:"flavor_name"`
		Portions   int       `json:"portions"`
	} `json:"items"`
}

// OrderCancelledData is the order.cancelled payload (order_id plus a reason code).
type OrderCancelledData struct {
	OrderID uuid.UUID `json:"order_id"`
	Reason  string    `json:"reason"`
}

func newEnvelope(eventType string, at time.Time, data any) (Envelope, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		SpecVersion:     SpecVersion,
		ID:              uuid.New(),
		Source:          Source,
		Type:            eventType,
		Time:            at.UTC(),
		DataContentType: DataContentType,
		Data:            raw,
	}, nil
}

func NewOrderReady(at time.Time, d OrderReadyData) (Envelope, error) {
	return newEnvelope(TypeOrderReady, at, d)
}

func NewOrderPickedUp(at time.Time, d OrderPickedUpData) (Envelope, error) {
	return newEnvelope(TypeOrderPickedUp, at, d)
}

// Parse decodes and validates an incoming structured CloudEvent.
func Parse(body []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return Envelope{}, err
	}
	if e.SpecVersion != SpecVersion {
		return Envelope{}, errors.New("unsupported specversion")
	}
	if e.ID == uuid.Nil {
		return Envelope{}, errors.New("missing event id")
	}
	if e.Type == "" || len(e.Data) == 0 {
		return Envelope{}, errors.New("missing type or data")
	}
	return e, nil
}
