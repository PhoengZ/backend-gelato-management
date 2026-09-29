package inventory

import (
	"errors"
	"github.com/google/uuid"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid input")
	ErrNotFound   = errors.New("resource not found")
	ErrConflict   = errors.New("idempotency or order conflict")
	ErrStock      = errors.New("insufficient available stock")
	ErrState      = errors.New("invalid or expired reservation state")
	ErrDependency = errors.New("dependency unavailable")
)

const MaxPortions int64 = math.MaxInt32

type Batch struct {
	ID             string    `json:"id"`
	FlavorID       string    `json:"flavor_id"`
	ProductionDate string    `json:"production_date"`
	ExpiresAt      time.Time `json:"expires_at"`
	Initial        int64     `json:"initial_portions"`
	Available      int64     `json:"available_portions"`
	Reserved       int64     `json:"reserved_portions"`
	Sold           int64     `json:"sold_portions"`
	Wasted         int64     `json:"wasted_portions"`
	UnitCost       int64     `json:"unit_cost_minor"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type CreateInput struct {
	FlavorID       string    `json:"flavor_id"`
	ProductionDate string    `json:"production_date"`
	ExpiresAt      time.Time `json:"expires_at"`
	Initial        int64     `json:"initial_portions"`
	UnitCost       *int64    `json:"unit_cost_minor"`
	Currency       string    `json:"currency"`
}
type Item struct {
	FlavorID string `json:"flavor_id"`
	Portions int64  `json:"portions"`
}
type Availability struct {
	FlavorID       string     `json:"flavor_id"`
	Available      int64      `json:"available_portions"`
	EarliestExpiry *time.Time `json:"earliest_expiry,omitempty"`
}
type Allocation struct {
	FlavorID string `json:"flavor_id"`
	BatchID  string `json:"batch_id"`
	Portions int64  `json:"portions"`
}
type Reservation struct {
	ID          string       `json:"reservation_id"`
	OrderID     string       `json:"order_id"`
	Status      string       `json:"status"`
	Allocations []Allocation `json:"allocations"`
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   time.Time    `json:"expires_at"`
}
type ReserveInput struct {
	OrderID string `json:"order_id"`
	Key     string `json:"idempotency_key"`
	Items   []Item `json:"items"`
}
type TransitionInput struct {
	ReservationID string `json:"reservation_id"`
	OrderID       string `json:"order_id"`
	Key           string `json:"idempotency_key"`
	Reason        string `json:"reason"`
}
type WasteInput struct {
	Portions int64  `json:"portions"`
	Reason   string `json:"reason"`
	Note     string `json:"note,omitempty"`
	Key      string `json:"idempotency_key"`
}
type WasteRecord struct {
	ID         string    `json:"id"`
	BatchID    string    `json:"batch_id"`
	FlavorID   string    `json:"flavor_id"`
	Portions   int64     `json:"portions"`
	Reason     string    `json:"reason"`
	Note       string    `json:"note,omitempty"`
	CostLost   int64     `json:"cost_lost_minor"`
	Currency   string    `json:"currency"`
	RecordedAt time.Time `json:"recorded_at"`
}

func ID(raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return "", ErrInvalid
	}
	return id.String(), nil
}
func validItems(items []Item) error {
	if len(items) == 0 || len(items) > 100 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for i := range items {
		id, err := ID(items[i].FlavorID)
		if err != nil || seen[id] || items[i].Portions < 1 || items[i].Portions > MaxPortions {
			return ErrInvalid
		}
		items[i].FlavorID = id
		seen[id] = true
	}
	return nil
}
func validReason(v string) bool {
	return v == "EXPIRED" || v == "DAMAGED" || v == "QUALITY" || v == "ADJUSTMENT"
}
func shortText(v string, max int) bool { return len([]rune(v)) <= max && !strings.ContainsRune(v, 0) }
