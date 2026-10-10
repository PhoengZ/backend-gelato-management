package notify

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// ownerMap remembers which customer owns an order. order.ready and
// order.cancelled carry only order_id, so the mapping learned from order.placed
// is the only way to route them without calling Order Service. It is bounded
// (oldest entries are evicted first) and lost on restart.
type ownerMap struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	now     func() time.Time
	entries map[uuid.UUID]ownerEntry
	order   []uuid.UUID // insertion order, used for eviction
}

type ownerEntry struct {
	customerID uuid.UUID
	expires    time.Time
}

func newOwnerMap(ttl time.Duration, max int, now func() time.Time) *ownerMap {
	return &ownerMap{ttl: ttl, max: max, now: now, entries: make(map[uuid.UUID]ownerEntry)}
}

func (m *ownerMap) put(orderID, customerID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[orderID]; !exists {
		m.order = append(m.order, orderID)
	}
	m.entries[orderID] = ownerEntry{customerID: customerID, expires: m.now().Add(m.ttl)}

	for len(m.entries) > m.max && len(m.order) > 0 {
		delete(m.entries, m.order[0])
		m.order = m.order[1:]
	}
}

func (m *ownerMap) get(orderID uuid.UUID) (uuid.UUID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[orderID]
	if !ok {
		return uuid.Nil, false
	}
	if !m.now().Before(e.expires) {
		delete(m.entries, orderID)
		return uuid.Nil, false
	}
	return e.customerID, true
}

// seenSet is a bounded set of processed event ids. Delivery is at-least-once,
// so a redelivered event id must not push the same popup twice. The set is
// in-memory only: after a restart a redelivery can produce one duplicate popup,
// which is harmless for a transient in-app message.
type seenSet struct {
	mu    sync.Mutex
	max   int
	ids   map[uuid.UUID]struct{}
	order []uuid.UUID
}

func newSeenSet(max int) *seenSet {
	return &seenSet{max: max, ids: make(map[uuid.UUID]struct{})}
}

// markNew records id and reports whether it was not seen before.
func (s *seenSet) markNew(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.ids[id]; dup {
		return false
	}
	s.ids[id] = struct{}{}
	s.order = append(s.order, id)
	for len(s.order) > s.max {
		delete(s.ids, s.order[0])
		s.order = s.order[1:]
	}
	return true
}
