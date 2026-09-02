package domain

import (
	"time"

	"gorm.io/gorm"
)

type OrderStatus string

const (
	StatusPendingPayment OrderStatus = "PENDING_PAYMENT"
	StatusPaid           OrderStatus = "PAID"
	StatusPreparing      OrderStatus = "PREPARING"
	StatusReadyForPickup OrderStatus = "READY_FOR_PICKUP"
	StatusCompleted      OrderStatus = "COMPLETED"
	StatusCancelled      OrderStatus = "CANCELLED"
)

type Order struct {
	ID             string         `gorm:"primaryKey;type:uuid;default:gen_random_uuid()" json:"id"`
	CustomerName   string         `json:"customerName"` // Simple customer identifier for MVP
	TimeSlot       string         `json:"timeSlot"`     // e.g. "14:00 - 14:15"
	Status         OrderStatus    `json:"status"`
	IdempotencyKey string         `gorm:"uniqueIndex" json:"-"`
	Items          []OrderItem    `gorm:"foreignKey:OrderID" json:"items"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

type OrderItem struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	OrderID   string    `gorm:"index" json:"orderId"`
	FlavorID  string    `json:"flavorId"`
	Portions  int       `json:"portions"`
	Price     float64   `json:"price"` // Price at the time of order
}

// Event Models for Message Broker

type OrderPlacedEvent struct {
	OrderID  string      `json:"orderId"`
	TimeSlot string      `json:"timeSlot"`
	Status   OrderStatus `json:"status"`
	Items    []OrderItem `json:"items"`
}

type OrderCancelledEvent struct {
	OrderID string `json:"orderId"`
}
