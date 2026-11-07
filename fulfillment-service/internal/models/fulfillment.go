package models

import (
	"time"

	"github.com/google/uuid"
)

// Status is the fulfillment ticket lifecycle:
// QUEUED -> PREPARING -> READY_FOR_PICKUP -> PICKED_UP, and QUEUED or
// PREPARING -> CANCELLED when the order is cancelled.
type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusPreparing Status = "PREPARING"
	StatusReady     Status = "READY_FOR_PICKUP"
	StatusPickedUp  Status = "PICKED_UP"
	StatusCancelled Status = "CANCELLED"
)

func (s Status) IsValid() bool {
	switch s {
	case StatusQueued, StatusPreparing, StatusReady, StatusPickedUp, StatusCancelled:
		return true
	}
	return false
}

// IsUpdatable reports whether staff may request this status through the API.
func (s Status) IsUpdatable() bool {
	return s == StatusPreparing || s == StatusReady || s == StatusPickedUp
}

// IsCancellable reports whether order.cancelled may still cancel a ticket in
// this status (the kitchen has not finished it yet).
func (s Status) IsCancellable() bool {
	return s == StatusQueued || s == StatusPreparing
}

// CanTransition reports whether a ticket may move from one status to another.
func CanTransition(from, to Status) bool {
	switch {
	case from == StatusQueued && to == StatusPreparing:
		return true
	case from == StatusPreparing && to == StatusReady:
		return true
	case from == StatusReady && to == StatusPickedUp:
		return true
	}
	return false
}

// Item is the order-line snapshot copied from the order.placed event.
type Item struct {
	FlavorID   uuid.UUID `json:"flavor_id"`
	FlavorName string    `json:"flavor_name"`
	Portions   int       `json:"portions"`
}

// Fulfillment is the kitchen ticket for one paid order.
type Fulfillment struct {
	ID           uuid.UUID  `json:"id"`
	OrderID      uuid.UUID  `json:"order_id"`
	CustomerID   *uuid.UUID `json:"-"` // ownership check only; not part of the response contract
	QueueNumber  string     `json:"queue_number"`
	PickupSlotID uuid.UUID  `json:"pickup_slot_id"`
	PickupAt     time.Time  `json:"pickup_at"`
	Status       Status     `json:"status"`
	Items        []Item     `json:"items"`
	PickedUpAt   *time.Time `json:"picked_up_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// UpdateStatusRequest is the body of PATCH /api/v1/fulfillments/:order_id/status.
type UpdateStatusRequest struct {
	Status Status `json:"status"`
}

// FulfillmentStatusHistory is an immutable audit log entry of a ticket state change.
type FulfillmentStatusHistory struct {
	ID            uuid.UUID  `json:"id"`
	FulfillmentID uuid.UUID  `json:"fulfillment_id"`
	OldStatus     *Status    `json:"old_status,omitempty"`
	NewStatus     Status     `json:"new_status"`
	ActorID       *uuid.UUID `json:"actor_id,omitempty"`
	SourceEventID *uuid.UUID `json:"source_event_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ListFilter narrows the kitchen queue. Nil or empty fields do not filter.
type ListFilter struct {
	Statuses   []Status
	PickupFrom *time.Time // inclusive
	PickupTo   *time.Time // exclusive
}

// SlotCapacityResponse represents active ticket capacity for a 15-minute slot window.
type SlotCapacityResponse struct {
	SlotTime          time.Time `json:"slot_time"`
	SlotStart         time.Time `json:"slot_start"`
	SlotEnd           time.Time `json:"slot_end"`
	ActiveTicketCount int       `json:"active_ticket_count"`
	ActiveCount       int       `json:"active_count"`
}
