// Package notify turns domain events into in-app notifications and decides who
// receives them. It is stateless with respect to business data: the only state
// is an in-memory order->customer map and a bounded set of processed event ids.
package notify

import (
	"log"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/auth"
	"notification-service/internal/events"
	"notification-service/internal/hub"
)

const (
	maxTrackedOrders = 50_000
	maxSeenEvents    = 50_000
)

// Notification types sent to clients. They are documented in
// contracts/openapi/notification-service.v1.yaml.
const (
	TypeOrderConfirmed = "order.confirmed"
	TypeOrderReady     = "order.ready"
	TypeOrderCancelled = "order.cancelled"
	TypeLowStock       = "inventory.low_stock"
	TypeExpiring       = "inventory.expiring"
)

// Pusher is the part of hub.Hub the service needs.
type Pusher interface {
	PushToUser(userID uuid.UUID, n hub.Notification) int
	PushToRoles(n hub.Notification, roles ...auth.Role) int
}

type Service struct {
	pusher Pusher
	owners *ownerMap
	seen   *seenSet
}

func New(p Pusher, orderOwnerTTL time.Duration) *Service {
	return newService(p, orderOwnerTTL, time.Now)
}

func newService(p Pusher, ttl time.Duration, now func() time.Time) *Service {
	return &Service{
		pusher: p,
		owners: newOwnerMap(ttl, maxTrackedOrders, now),
		seen:   newSeenSet(maxSeenEvents),
	}
}

// OrderPlaced records who owns the order. It pushes nothing: the customer's
// confirmation comes from fulfillment.order_queued, which carries the queue
// number, so the customer gets exactly one confirmation.
func (s *Service) OrderPlaced(env events.Envelope, d events.OrderPlacedData) {
	if !s.seen.markNew(env.ID) {
		return
	}
	s.owners.put(d.OrderID, d.CustomerID)
}

// OrderQueued confirms the order to the customer with the queue number that
// Fulfillment assigned when it created the ticket.
func (s *Service) OrderQueued(env events.Envelope, d events.OrderQueuedData) {
	if !s.seen.markNew(env.ID) {
		return
	}
	s.owners.put(d.OrderID, d.CustomerID)

	s.pusher.PushToUser(d.CustomerID, hub.Notification{
		ID:         env.ID,
		Type:       TypeOrderConfirmed,
		Message:    "Your gelato order is confirmed.",
		OccurredAt: env.Time.UTC(),
		Data: map[string]any{
			"order_id":       d.OrderID,
			"queue_number":   d.QueueNumber,
			"pickup_at":      d.PickupAt.UTC(),
			"pickup_slot_id": d.PickupSlotID,
		},
	})
}

// OrderReady tells the customer the order can be collected.
func (s *Service) OrderReady(env events.Envelope, d events.OrderReadyData) {
	if !s.seen.markNew(env.ID) {
		return
	}
	s.toOrderOwner(d.OrderID, d.CustomerID, hub.Notification{
		ID:         env.ID,
		Type:       TypeOrderReady,
		Message:    "Your gelato order is ready for pickup!",
		OccurredAt: env.Time.UTC(),
		Data: map[string]any{
			"order_id":       d.OrderID,
			"queue_number":   d.QueueNumber,
			"pickup_slot_id": d.PickupSlotID,
		},
	})
}

// OrderCancelled tells the customer their order was cancelled.
func (s *Service) OrderCancelled(env events.Envelope, d events.OrderCancelledData) {
	if !s.seen.markNew(env.ID) {
		return
	}
	s.toOrderOwner(d.OrderID, d.CustomerID, hub.Notification{
		ID:         env.ID,
		Type:       TypeOrderCancelled,
		Message:    "Your gelato order was cancelled.",
		OccurredAt: env.Time.UTC(),
		Data: map[string]any{
			"order_id": d.OrderID,
			"reason":   d.Reason,
		},
	})
}

// BatchLowStock alerts kitchen staff and managers.
func (s *Service) BatchLowStock(env events.Envelope, d events.BatchLowStockData) {
	if !s.seen.markNew(env.ID) {
		return
	}
	s.pusher.PushToRoles(hub.Notification{
		ID:         env.ID,
		Type:       TypeLowStock,
		Message:    "A gelato batch is running low.",
		OccurredAt: env.Time.UTC(),
		Data: map[string]any{
			"batch_id":           d.BatchID,
			"flavor_id":          d.FlavorID,
			"remaining_portions": d.RemainingPortions,
			"threshold_portions": d.ThresholdPortions,
		},
	}, auth.RoleStaff, auth.RoleManager)
}

// BatchExpiring alerts kitchen staff and managers.
func (s *Service) BatchExpiring(env events.Envelope, d events.BatchExpiringData) {
	if !s.seen.markNew(env.ID) {
		return
	}
	s.pusher.PushToRoles(hub.Notification{
		ID:         env.ID,
		Type:       TypeExpiring,
		Message:    "A gelato batch is about to expire.",
		OccurredAt: env.Time.UTC(),
		Data: map[string]any{
			"batch_id":   d.BatchID,
			"flavor_id":  d.FlavorID,
			"expires_at": d.ExpiresAt.UTC(),
		},
	}, auth.RoleStaff, auth.RoleManager)
}

// toOrderOwner routes n to the event's customer. Events published before
// customer_id existed fall back to the mapping learned from order.placed; an order
// unknown to both is logged and skipped because retrying cannot help.
func (s *Service) toOrderOwner(orderID, customerID uuid.UUID, n hub.Notification) {
	if customerID == uuid.Nil {
		owner, ok := s.owners.get(orderID)
		if !ok {
			log.Printf("[notify] no known owner for order %s, dropping %s", orderID, n.Type)
			return
		}
		customerID = owner
	}
	s.pusher.PushToUser(customerID, n)
}
