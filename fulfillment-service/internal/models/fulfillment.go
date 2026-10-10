package models

import (
	"time"

	"github.com/google/uuid"
)

// TicketStatus mirrors the lifecycle Fulfillment Service owns:
// PREPARING -> READY_FOR_PICKUP -> PICKED_UP
type TicketStatus string

const (
	StatusPreparing TicketStatus = "PREPARING"
	StatusReady     TicketStatus = "READY_FOR_PICKUP"
	StatusPickedUp  TicketStatus = "PICKED_UP"
)

func (s TicketStatus) IsValid() bool {
	switch s {
	case StatusPreparing, StatusReady, StatusPickedUp:
		return true
	}
	return false
}

// KitchenTicket is the resource this assignment's REST API is built around.
type KitchenTicket struct {
	ID          uuid.UUID    `json:"id"`
	OrderID     string       `json:"order_id"`
	PickupSlot  string       `json:"pickup_slot"`
	QueueNumber int          `json:"queue_number"`
	Status      TicketStatus `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// CreateTicketRequest is the JSON body for POST /tickets
type CreateTicketRequest struct {
	OrderID     string `json:"order_id" validate:"required"`
	PickupSlot  string `json:"pickup_slot" validate:"required"`
	QueueNumber int    `json:"queue_number" validate:"required,gt=0"`
}

// UpdateTicketRequest is the JSON body for PUT /tickets/:id
// Only status is mutable through this endpoint (matches markOrderReady /
// markOrderPickedUp in the real Fulfillment Service).
type UpdateTicketRequest struct {
	Status TicketStatus `json:"status" validate:"required"`
}
