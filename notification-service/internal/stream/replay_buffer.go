// Package stream provides buffering and replay capabilities for Server-Sent Events (SSE).
package stream

import (
	"sync"
	"time"

	"github.com/google/uuid"

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

// customerRingBuffer is a circular ring buffer storing up to cap notifications for a single customer.
type customerRingBuffer struct {
	items []bufferedItem
	head  int // next write position
	count int // number of elements currently stored
	cap   int // maximum capacity
}

func newCustomerRingBuffer(capacity int) *customerRingBuffer {
	if capacity <= 0 {
		capacity = DefaultMaxEventsPerCustomer
	}
	return &customerRingBuffer{
		items: make([]bufferedItem, capacity),
		cap:   capacity,
	}
}

// add stores a notification in the circular ring buffer, overwriting the oldest item if full.
func (rb *customerRingBuffer) add(n hub.Notification, now time.Time) {
	it := bufferedItem{notification: n, addedAt: now}
	if rb.count < rb.cap {
		rb.items[rb.head] = it
		rb.head = (rb.head + 1) % rb.cap
		rb.count++
	} else {
		// Overwrite the oldest element at head
		rb.items[rb.head] = it
		rb.head = (rb.head + 1) % rb.cap
	}
}

// contains checks if a notification with the given ID is already in the buffer.
func (rb *customerRingBuffer) contains(id uuid.UUID) bool {
	for i := 0; i < rb.count; i++ {
		idx := i
		if rb.count == rb.cap {
			idx = (rb.head + i) % rb.cap
		}
		if rb.items[idx].notification.ID == id {
			return true
		}
	}
	return false
}

// events returns all non-expired notifications in chronological order (oldest to newest).
func (rb *customerRingBuffer) events(now time.Time, ttl time.Duration) []hub.Notification {
	if rb.count == 0 {
		return nil
	}
	var valid []hub.Notification
	for i := 0; i < rb.count; i++ {
		idx := i
		if rb.count == rb.cap {
			idx = (rb.head + i) % rb.cap
		}
		it := rb.items[idx]
		if now.Sub(it.addedAt) > ttl {
			continue // expired
		}
		valid = append(valid, it.notification)
	}
	return valid
}

// hasValid checks if there are any non-expired items in the buffer.
func (rb *customerRingBuffer) hasValid(now time.Time, ttl time.Duration) bool {
	for i := 0; i < rb.count; i++ {
		idx := i
		if rb.count == rb.cap {
			idx = (rb.head + i) % rb.cap
		}
		if now.Sub(rb.items[idx].addedAt) <= ttl {
			return true
		}
	}
	return false
}

// ReplayBuffer is a thread-safe, bounded in-memory ring buffer storing dispatched notifications
// partitioned by customer ID with a maximum event capacity and TTL.
type ReplayBuffer struct {
	mu        sync.RWMutex
	customers map[uuid.UUID]*customerRingBuffer
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

// Replay returns all non-expired notifications for customerID that occurred after lastEventID.
//   - If lastEventID is uuid.Nil, all valid non-expired notifications for that customer are returned.
//   - If lastEventID is found in the buffer, only subsequent valid notifications are returned.
//   - If lastEventID is not found in the buffer (i.e. older than buffer or evicted), all valid
//     non-expired notifications are returned to maximize delivery.
//   - If lastEventID matches the latest notification, an empty slice is returned.
func (b *ReplayBuffer) Replay(customerID uuid.UUID, lastEventID uuid.UUID) []hub.Notification {
	b.mu.RLock()
	defer b.mu.RUnlock()

	crb, ok := b.customers[customerID]
	if !ok {
		return nil
	}

	all := crb.events(b.now(), b.ttl)
	if len(all) == 0 {
		return nil
	}

	if lastEventID == uuid.Nil {
		return all
	}

	foundIdx := -1
	for i, n := range all {
		if n.ID == lastEventID {
			foundIdx = i
			break
		}
	}

	if foundIdx >= 0 {
		if foundIdx+1 < len(all) {
			res := make([]hub.Notification, len(all)-(foundIdx+1))
			copy(res, all[foundIdx+1:])
			return res
		}
		return nil
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
	return len(crb.events(b.now(), b.ttl))
}

// CleanExpired removes customer entries that contain no valid non-expired events.
func (b *ReplayBuffer) CleanExpired() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	for id, crb := range b.customers {
		if !crb.hasValid(now, b.ttl) {
			delete(b.customers, id)
		}
	}
}

// Clear removes all stored notifications across all customers.
func (b *ReplayBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.customers = make(map[uuid.UUID]*customerRingBuffer)
}
