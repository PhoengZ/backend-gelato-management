// Package events holds the CloudEvents 1.0 envelopes Notification consumes.
// Shapes follow contracts/events/ and docs/event-schema-spec.md. Notification
// publishes nothing, and unknown fields are ignored so compatible additions do
// not break the consumer.
package events

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	SpecVersion = "1.0"

	TypeOrderPlaced    = "com.gelatoflow.order.placed.v1"
	TypeOrderCancelled = "com.gelatoflow.order.cancelled.v1"
	TypeOrderReady     = "com.gelatoflow.fulfillment.order-ready.v1"
	TypeBatchLowStock  = "com.gelatoflow.inventory.batch-low-stock.v1"
	TypeBatchExpiring  = "com.gelatoflow.inventory.batch-expiring.v1"

	RoutingKeyOrderPlaced   = "order.placed"
	RoutingKeyOrderCancel   = "order.cancelled"
	RoutingKeyOrderReady    = "fulfillment.order_ready"
	RoutingKeyBatchLowStock = "inventory.low_stock"
	RoutingKeyBatchExpiring = "inventory.expiring"
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

// OrderItem is one line of an order.placed payload.
type OrderItem struct {
	FlavorID   uuid.UUID `json:"flavor_id"`
	FlavorName string    `json:"flavor_name"`
	Portions   int       `json:"portions"`
}

// OrderPlacedData is the order.placed payload. It is the only event that
// carries customer_id, so it is also how Notification learns who owns an order.
type OrderPlacedData struct {
	OrderID          uuid.UUID   `json:"order_id"`
	CustomerID       uuid.UUID   `json:"customer_id"`
	PickupAt         time.Time   `json:"pickup_at"`
	TotalAmountMinor int64       `json:"total_amount_minor"`
	Currency         string      `json:"currency"`
	Items            []OrderItem `json:"items"`
}

// OrderCancelledData is the order.cancelled payload (order_id plus a reason code).
type OrderCancelledData struct {
	OrderID uuid.UUID `json:"order_id"`
	Reason  string    `json:"reason"`
}

// OrderReadyData is the fulfillment.order_ready payload. It has no customer_id.
type OrderReadyData struct {
	OrderID      uuid.UUID `json:"order_id"`
	QueueNumber  string    `json:"queue_number"`
	PickupSlotID uuid.UUID `json:"pickup_slot_id"`
}

// BatchLowStockData is the inventory.low_stock payload. docs/event-schema-spec.md
// describes it but no JSON schema exists yet, so the shape is unvalidated.
type BatchLowStockData struct {
	BatchID           uuid.UUID `json:"batch_id"`
	FlavorID          uuid.UUID `json:"flavor_id"`
	RemainingPortions int       `json:"remaining_portions"`
	ThresholdPortions int       `json:"threshold_portions"`
}

// BatchExpiringData is the inventory.expiring payload (also without a schema yet).
type BatchExpiringData struct {
	BatchID   uuid.UUID `json:"batch_id"`
	FlavorID  uuid.UUID `json:"flavor_id"`
	ExpiresAt time.Time `json:"expires_at"`
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
