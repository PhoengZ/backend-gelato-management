// Package stream provides buffering and replay capabilities for Server-Sent Events (SSE).
package stream

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/auth"
	"notification-service/internal/hub"
)

const (
	// DefaultMaxEventsPerCustomer is the maximum number of dispatched notifications
	// retained in the ring buffer per customer.
	DefaultMaxEventsPerCustomer = 500

	// DefaultTTL is the maximum time-to-live for a buffered notification.
	DefaultTTL = 5 * time.Minute
)

// bufferedItem holds a notification along with the timestamp it was buffered.
type bufferedItem struct {
	notification hub.Notification
	addedAt      time.Time
}

// customerRingBuffer is a circular ring buffer storing up to cap notifications for a single
// customer (or role). Slots are allocated as they are used, so a user with one notification
// costs one slot rather than cap.
type customerRingBuffer struct {
	items []bufferedItem
	head  int // oldest element once the buffer is full
	cap   int // maximum capacity
}

func newCustomerRingBuffer(capacity int) *customerRingBuffer {
	if capacity <= 0 {
		capacity = DefaultMaxEventsPerCustomer
	}
	return &customerRingBuffer{cap: capacity}
}

// add stores a notification in the circular ring buffer, overwriting the oldest item if full.
func (rb *customerRingBuffer) add(n hub.Notification, now time.Time) {
	it := bufferedItem{notification: n, addedAt: now}
	if len(rb.items) < rb.cap {
		rb.items = append(rb.items, it)
		return
	}
	rb.items[rb.head] = it
	rb.head = (rb.head + 1) % rb.cap
}

// at returns the i-th item in chronological order (oldest first).
func (rb *customerRingBuffer) at(i int) bufferedItem {
	return rb.items[(rb.head+i)%len(rb.items)]
}

// contains checks if a notification with the given ID is already in the buffer.
func (rb *customerRingBuffer) contains(id uuid.UUID) bool {
	for i := range rb.items {
		if rb.items[i].notification.ID == id {
			return true
		}
	}
	return false
}

// valid returns all non-expired items in chronological order (oldest to newest).
func (rb *customerRingBuffer) valid(now time.Time, ttl time.Duration) []bufferedItem {
	var out []bufferedItem
	for i := range rb.items {
		it := rb.at(i)
		if now.Sub(it.addedAt) > ttl {
			continue // expired
		}
		out = append(out, it)
	}
	return out
}

// hasValid checks if there are any non-expired items in the buffer.
func (rb *customerRingBuffer) hasValid(now time.Time, ttl time.Duration) bool {
	for i := range rb.items {
		if now.Sub(rb.items[i].addedAt) <= ttl {
			return true
		}
	}
	return false
}

// ReplayBuffer is a thread-safe, bounded in-memory ring buffer storing dispatched notifications
// partitioned by customer ID (and by role for broadcast alerts) with a maximum event capacity and TTL.
type ReplayBuffer struct {
	mu        sync.RWMutex
	customers map[uuid.UUID]*customerRingBuffer
	roles     map[auth.Role]*customerRingBuffer
	maxEvents int
	ttl       time.Duration
	now       func() time.Time
}

// NewReplayBuffer creates a new ReplayBuffer with the specified max capacity per customer and TTL.
// If maxEvents <= 0 or ttl <= 0, defaults (500 events, 5 minutes) are used.
func NewReplayBuffer(maxEvents int, ttl time.Duration) *ReplayBuffer {
	return newReplayBufferWithClock(maxEvents, ttl, time.Now)
}

// NewDefaultReplayBuffer creates a ReplayBuffer with default settings (500 events per customer, 5-minute TTL).
func NewDefaultReplayBuffer() *ReplayBuffer {
	return NewReplayBuffer(DefaultMaxEventsPerCustomer, DefaultTTL)
}

// newReplayBufferWithClock creates a ReplayBuffer with a injectable clock for deterministic testing.
func newReplayBufferWithClock(maxEvents int, ttl time.Duration, clock func() time.Time) *ReplayBuffer {
	if maxEvents <= 0 {
		maxEvents = DefaultMaxEventsPerCustomer
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if clock == nil {
		clock = time.Now
	}
	return &ReplayBuffer{
		customers: make(map[uuid.UUID]*customerRingBuffer),
		roles:     make(map[auth.Role]*customerRingBuffer),
		maxEvents: maxEvents,
		ttl:       ttl,
		now:       clock,
	}
}

// Add appends a dispatched notification for a customer to the ring buffer.
// If the customer buffer is full (reaches maxEvents), the oldest notification is overwritten.
func (b *ReplayBuffer) Add(customerID uuid.UUID, n hub.Notification) {
	if customerID == uuid.Nil || n.ID == uuid.Nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	crb, ok := b.customers[customerID]
	if !ok {
		crb = newCustomerRingBuffer(b.maxEvents)
		b.customers[customerID] = crb
	}

	// Avoid duplicate entries if the same event ID is dispatched again
	if crb.contains(n.ID) {
		return
	}

	crb.add(n, b.now())
}

// AddForRoles appends a notification broadcast to roles (e.g. low-stock alerts for STAFF and
// MANAGER) so those users can replay it after reconnecting.
func (b *ReplayBuffer) AddForRoles(n hub.Notification, roles ...auth.Role) {
	if n.ID == uuid.Nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for _, r := range roles {
		rrb, ok := b.roles[r]
		if !ok {
			rrb = newCustomerRingBuffer(b.maxEvents)
			b.roles[r] = rrb
		}
		if !rrb.contains(n.ID) {
			rrb.add(n, b.now())
		}
	}
}

// Replay returns all non-expired notifications for customerID that occurred after lastEventID.
//   - If lastEventID is uuid.Nil, all valid non-expired notifications for that customer are returned.
//   - If lastEventID is found in the buffer, only subsequent valid notifications are returned.
//   - If lastEventID is not found in the buffer (i.e. older than buffer or evicted), all valid
//     non-expired notifications are returned to maximize delivery.
//   - If lastEventID matches the latest notification, an empty slice is returned.
func (b *ReplayBuffer) Replay(customerID uuid.UUID, lastEventID uuid.UUID) []hub.Notification {
	return b.ReplayFor(customerID, "", lastEventID)
}

// ReplayFor is Replay for a connection that also receives broadcasts for role. Notifications
// addressed to the user and to the role are merged in the order they were dispatched.
func (b *ReplayBuffer) ReplayFor(userID uuid.UUID, role auth.Role, lastEventID uuid.UUID) []hub.Notification {
	b.mu.RLock()
	defer b.mu.RUnlock()

	now := b.now()
	var items []bufferedItem
	if crb, ok := b.customers[userID]; ok {
		items = append(items, crb.valid(now, b.ttl)...)
	}
	if rrb, ok := b.roles[role]; ok && role != "" {
		items = append(items, rrb.valid(now, b.ttl)...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].addedAt.Before(items[j].addedAt) })
	}
	if len(items) == 0 {
		return nil
	}

	all := make([]hub.Notification, len(items))
	for i, it := range items {
		all[i] = it.notification
	}

	if lastEventID == uuid.Nil {
		return all
	}

	for i, n := range all {
		if n.ID == lastEventID {
			if i+1 < len(all) {
				return all[i+1:]
			}
			return nil
		}
	}

	// lastEventID was not found (evicted/older than buffer retention window): return all available
	return all
}

// GetAll returns all non-expired notifications for the specified customer.
func (b *ReplayBuffer) GetAll(customerID uuid.UUID) []hub.Notification {
	return b.Replay(customerID, uuid.Nil)
}

// Size returns the count of non-expired notifications stored for the specified customer.
func (b *ReplayBuffer) Size(customerID uuid.UUID) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	crb, ok := b.customers[customerID]
	if !ok {
		return 0
	}
	return len(crb.valid(b.now(), b.ttl))
}

// CleanExpired removes customer and role entries that contain no valid non-expired events.
// Call it periodically; without it the map keeps one entry per user ever notified.
func (b *ReplayBuffer) CleanExpired() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	for id, crb := range b.customers {
		if !crb.hasValid(now, b.ttl) {
			delete(b.customers, id)
		}
	}
	for r, rrb := range b.roles {
		if !rrb.hasValid(now, b.ttl) {
			delete(b.roles, r)
		}
	}
}

// Clear removes all stored notifications across all customers.
func (b *ReplayBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customers = make(map[uuid.UUID]*customerRingBuffer)
	b.roles = make(map[auth.Role]*customerRingBuffer)
}
