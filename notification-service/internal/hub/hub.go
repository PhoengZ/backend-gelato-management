// Package hub fans notifications out to the live SSE connections of the
// Notification Service. It is the in-process implementation of the course
// deliverable's pushInAppNotification(): nothing is stored, so a notification
// only reaches users who are connected when it is pushed.
package hub

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/auth"
)

// Notification is the payload written to the client as one SSE frame.
type Notification struct {
	ID         uuid.UUID `json:"id"`          // id of the source event, also the SSE id
	Type       string    `json:"type"`        // e.g. order.ready
	Message    string    `json:"message"`     // human-readable popup text
	OccurredAt time.Time `json:"occurred_at"` // source event time, RFC 3339 UTC
	Data       any       `json:"data"`        // type-specific fields, snake_case
}

// Subscriber is one live connection.
type Subscriber struct {
	userID uuid.UUID
	role   auth.Role
	ch     chan Notification
}

// C delivers notifications to the connection handler.
func (s *Subscriber) C() <-chan Notification { return s.ch }

type Hub struct {
	mu     sync.RWMutex
	subs   map[*Subscriber]struct{}
	buffer int
}

// New creates a Hub whose subscribers each buffer up to buffer notifications.
func New(buffer int) *Hub {
	if buffer < 1 {
		buffer = 1
	}
	return &Hub{subs: make(map[*Subscriber]struct{}), buffer: buffer}
}

// Subscribe registers a connection and returns it with a cancel func that must
// be called when the connection ends.
func (h *Hub) Subscribe(userID uuid.UUID, role auth.Role) (*Subscriber, func()) {
	s := &Subscriber{userID: userID, role: role, ch: make(chan Notification, h.buffer)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()

	return s, func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}
}

// PushToUser delivers n to every connection of one user and returns how many
// connections received it.
func (h *Hub) PushToUser(userID uuid.UUID, n Notification) int {
	return h.push(n, func(s *Subscriber) bool { return s.userID == userID })
}

// PushToRoles delivers n to every connection whose role is in roles.
func (h *Hub) PushToRoles(n Notification, roles ...auth.Role) int {
	return h.push(n, func(s *Subscriber) bool {
		for _, r := range roles {
			if s.role == r {
				return true
			}
		}
		return false
	})
}

func (h *Hub) push(n Notification, match func(*Subscriber) bool) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	delivered := 0
	for s := range h.subs {
		if match(s) && s.offer(n) {
			delivered++
		}
	}
	return delivered
}

// offer never blocks the broker consumer: if a slow client's buffer is full the
// oldest queued notification is dropped to make room.
func (s *Subscriber) offer(n Notification) bool {
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case s.ch <- n:
			return true
		default:
		}
		select {
		case <-s.ch:
		default:
		}
	}
	return false
}
