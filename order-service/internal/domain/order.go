package domain

import (
	"encoding/json"
	"time"
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
	ID                    string      `gorm:"primaryKey;type:uuid" json:"order_id"`
	CustomerID            string      `gorm:"type:uuid;not null;uniqueIndex:idx_customer_idempotency" json:"customer_id"`
	PickupAt              time.Time   `gorm:"not null" json:"pickup_at"`
	Status                OrderStatus `gorm:"not null;index" json:"status"`
	IdempotencyKey        string      `gorm:"not null;uniqueIndex:idx_customer_idempotency;size:255" json:"-"`
	RequestHash           string      `gorm:"not null;size:64" json:"-"`
	ReservationID         string      `gorm:"type:uuid;not null;uniqueIndex" json:"-"`
	ReservationExpiresAt  time.Time   `json:"reservation_expires_at"`
	ConfirmIdempotencyKey string      `gorm:"not null;size:36" json:"-"`
	ReleaseIdempotencyKey string      `gorm:"not null;size:36" json:"-"`
	TotalAmountMinor      int64       `gorm:"not null" json:"total_amount_minor"`
	Currency              string      `gorm:"not null;size:3" json:"currency"`
	Items                 []OrderItem `gorm:"foreignKey:OrderID;constraint:OnDelete:CASCADE" json:"items"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type OrderItem struct {
	ID             uint   `gorm:"primaryKey" json:"-"`
	OrderID        string `gorm:"type:uuid;not null;index" json:"-"`
	FlavorID       string `gorm:"type:uuid;not null" json:"flavor_id"`
	FlavorName     string `gorm:"not null" json:"flavor_name"`
	Portions       int32  `gorm:"not null" json:"portions"`
	UnitPriceMinor int64  `gorm:"not null" json:"unit_price_minor"`
	SubtotalMinor  int64  `gorm:"not null" json:"subtotal_minor"`
}

type OutboxEvent struct {
	ID          string          `gorm:"primaryKey;type:uuid" json:"id"`
	OrderID     string          `gorm:"type:uuid;not null;index" json:"order_id"`
	RoutingKey  string          `gorm:"not null;size:128" json:"routing_key"`
	Payload     json.RawMessage `gorm:"type:jsonb;not null" json:"payload"`
	PublishedAt *time.Time      `gorm:"index" json:"published_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}
