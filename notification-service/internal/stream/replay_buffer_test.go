package stream

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/auth"
	"notification-service/internal/hub"
)

func makeNotification(id uuid.UUID, nType string, msg string) hub.Notification {
	return hub.Notification{
		ID:         id,
		Type:       nType,
		Message:    msg,
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"test": true},
	}
}

func TestReplayBufferBasicAddAndGetAll(t *testing.T) {
	buf := NewDefaultReplayBuffer()
	customer := uuid.New()

	id1 := uuid.New()
	id2 := uuid.New()
	buf.Add(customer, makeNotification(id1, "order.confirmed", "First"))
	buf.Add(customer, makeNotification(id2, "order.ready", "Second"))

	events := buf.GetAll(customer)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != id1 || events[0].Type != "order.confirmed" {
		t.Errorf("expected first event id %s, got %s", id1, events[0].ID)
	}
	if events[1].ID != id2 || events[1].Type != "order.ready" {
		t.Errorf("expected second event id %s, got %s", id2, events[1].ID)
	}
	if buf.Size(customer) != 2 {
		t.Errorf("expected size 2, got %d", buf.Size(customer))
	}
}

func TestReplayBufferCapacityEvictsOldest(t *testing.T) {
	// Create a buffer with capacity 5 for easy testing
	buf := NewReplayBuffer(5, 5*time.Minute)
	customer := uuid.New()

	var ids []uuid.UUID
	for i := 0; i < 8; i++ {
		id := uuid.New()
		ids = append(ids, id)
		buf.Add(customer, makeNotification(id, "order.confirmed", fmt.Sprintf("Event %d", i)))
	}

	if buf.Size(customer) != 5 {
		t.Fatalf("expected size 5, got %d", buf.Size(customer))
	}

	events := buf.GetAll(customer)
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	// Should have kept ids[3] through ids[7] (last 5)
	for i := 0; i < 5; i++ {
		expectedID := ids[3+i]
		if events[i].ID != expectedID {
			t.Errorf("at index %d: expected %s, got %s", i, expectedID, events[i].ID)
		}
	}
}

func TestReplayBufferCapacity500(t *testing.T) {
	buf := NewReplayBuffer(500, 5*time.Minute)
	customer := uuid.New()

	var ids []uuid.UUID
	for i := 0; i < 600; i++ {
		id := uuid.New()
		ids = append(ids, id)
		buf.Add(customer, makeNotification(id, "order.confirmed", fmt.Sprintf("Event %d", i)))
	}

	if buf.Size(customer) != 500 {
		t.Fatalf("expected size 500, got %d", buf.Size(customer))
	}

	events := buf.GetAll(customer)
	if len(events) != 500 {
		t.Fatalf("expected 500 events, got %d", len(events))
	}

	// Oldest 100 should be evicted, keeping ids[100] to ids[599]
	for i := 0; i < 500; i++ {
		expectedID := ids[100+i]
		if events[i].ID != expectedID {
			t.Fatalf("at index %d: expected %s, got %s", i, expectedID, events[i].ID)
		}
	}
}

func TestReplayBufferTTL(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	buf := newReplayBufferWithClock(500, 5*time.Minute, clock)
	customer := uuid.New()

	id1 := uuid.New()
	id2 := uuid.New()
	buf.Add(customer, makeNotification(id1, "order.confirmed", "First"))

	// Advance clock by 3 minutes
	now = now.Add(3 * time.Minute)
	buf.Add(customer, makeNotification(id2, "order.ready", "Second"))

	// Both should still be valid (age 3m and 0m)
	if buf.Size(customer) != 2 {
		t.Fatalf("expected 2 valid events, got %d", buf.Size(customer))
	}

	// Advance clock by 3 more minutes (total 6m since id1, 3m since id2)
	now = now.Add(3 * time.Minute)

	// id1 should be expired (6m > 5m TTL), id2 still valid (3m < 5m)
	events := buf.GetAll(customer)
	if len(events) != 1 {
		t.Fatalf("expected 1 valid event, got %d", len(events))
	}
	if events[0].ID != id2 {
		t.Errorf("expected remaining event to be id2 (%s), got %s", id2, events[0].ID)
	}

	// Advance clock by another 3 minutes (total 6m since id2)
	now = now.Add(3 * time.Minute)

	events = buf.GetAll(customer)
	if len(events) != 0 {
		t.Fatalf("expected 0 valid events, got %d", len(events))
	}
}

func TestReplayBufferReplayFromLastEventID(t *testing.T) {
	buf := NewReplayBuffer(10, 5*time.Minute)
	customer := uuid.New()

	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()
	id4 := uuid.New()

	buf.Add(customer, makeNotification(id1, "event1", "First"))
	buf.Add(customer, makeNotification(id2, "event2", "Second"))
	buf.Add(customer, makeNotification(id3, "event3", "Third"))
	buf.Add(customer, makeNotification(id4, "event4", "Fourth"))

	// Replay after id2: should return id3 and id4
	missed := buf.Replay(customer, id2)
	if len(missed) != 2 {
		t.Fatalf("expected 2 missed events, got %d", len(missed))
	}
	if missed[0].ID != id3 || missed[1].ID != id4 {
		t.Errorf("expected [id3, id4], got [%s, %s]", missed[0].ID, missed[1].ID)
	}

	// Replay after id4 (latest event): should return nil/empty
	missedLatest := buf.Replay(customer, id4)
	if len(missedLatest) != 0 {
		t.Errorf("expected 0 missed events when last event is latest, got %d", len(missedLatest))
	}

	// Replay after unknown / evicted event ID: should return all available
	unknownID := uuid.New()
	missedUnknown := buf.Replay(customer, unknownID)
	if len(missedUnknown) != 4 {
		t.Errorf("expected all 4 events when last event is unknown, got %d", len(missedUnknown))
	}

	// Replay with uuid.Nil: should return all available
	missedNil := buf.Replay(customer, uuid.Nil)
	if len(missedNil) != 4 {
		t.Errorf("expected all 4 events with nil UUID, got %d", len(missedNil))
	}

	// Replay for unknown customer: should return nil
	missedOther := buf.Replay(uuid.New(), id2)
	if len(missedOther) != 0 {
		t.Errorf("expected 0 events for unknown customer, got %d", len(missedOther))
	}
}

func TestReplayBufferDeduplication(t *testing.T) {
	buf := NewDefaultReplayBuffer()
	customer := uuid.New()
	id := uuid.New()

	n := makeNotification(id, "order.confirmed", "First")
	buf.Add(customer, n)
	buf.Add(customer, n) // duplicate

	if buf.Size(customer) != 1 {
		t.Errorf("expected 1 event after duplicate add, got %d", buf.Size(customer))
	}
}

func TestReplayBufferCleanExpiredAndClear(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	buf := newReplayBufferWithClock(10, 5*time.Minute, clock)

	c1, c2 := uuid.New(), uuid.New()
	buf.Add(c1, makeNotification(uuid.New(), "type", "msg"))

	now = now.Add(6 * time.Minute)
	buf.Add(c2, makeNotification(uuid.New(), "type", "msg"))

	// c1's events are expired, c2's are fresh
	buf.CleanExpired()

	if buf.Size(c1) != 0 {
		t.Errorf("c1 should have 0 events after CleanExpired, got %d", buf.Size(c1))
	}
	if buf.Size(c2) != 1 {
		t.Errorf("c2 should have 1 event, got %d", buf.Size(c2))
	}

	buf.Clear()
	if buf.Size(c2) != 0 {
		t.Errorf("c2 should have 0 events after Clear, got %d", buf.Size(c2))
	}
}

func TestReplayBufferConcurrency(t *testing.T) {
	buf := NewDefaultReplayBuffer()
	customers := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}

	var wg sync.WaitGroup
	// Concurrently add events
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c := customers[(workerID+j)%len(customers)]
				buf.Add(c, makeNotification(uuid.New(), "test", fmt.Sprintf("msg-%d-%d", workerID, j)))
			}
		}(i)
	}

	// Concurrently replay events
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c := customers[(workerID+j)%len(customers)]
				_ = buf.Replay(c, uuid.Nil)
				_ = buf.Size(c)
			}
		}(i)
	}

	wg.Wait()
}

func TestReplayBufferAllocatesSlotsLazily(t *testing.T) {
	buf := NewReplayBuffer(500, 5*time.Minute)
	customer := uuid.New()
	buf.Add(customer, makeNotification(uuid.New(), "order.confirmed", "Only"))

	if got := cap(buf.customers[customer].items); got >= 500 {
		t.Errorf("one notification allocated %d slots, want far fewer than 500", got)
	}
}

func TestReplayBufferRoleBroadcastsMergeWithUserEvents(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	buf := newReplayBufferWithClock(10, 5*time.Minute, clock)
	manager := uuid.New()

	userEvt, roleEvt1, roleEvt2 := uuid.New(), uuid.New(), uuid.New()
	buf.AddForRoles(makeNotification(roleEvt1, "inventory.low_stock", "Low"), auth.RoleStaff, auth.RoleManager)
	now = now.Add(time.Second)
	buf.Add(manager, makeNotification(userEvt, "order.confirmed", "Mine"))
	now = now.Add(time.Second)
	buf.AddForRoles(makeNotification(roleEvt2, "inventory.expiring", "Expiring"), auth.RoleManager)

	got := buf.ReplayFor(manager, auth.RoleManager, uuid.Nil)
	if len(got) != 3 || got[0].ID != roleEvt1 || got[1].ID != userEvt || got[2].ID != roleEvt2 {
		t.Fatalf("merged replay = %v, want [role1, user, role2] in dispatch order", got)
	}
	if after := buf.ReplayFor(manager, auth.RoleManager, userEvt); len(after) != 1 || after[0].ID != roleEvt2 {
		t.Errorf("replay after user event = %v, want [role2]", after)
	}

	// Customers never see staff broadcasts, and Replay stays user-only.
	if got := buf.ReplayFor(uuid.New(), auth.RoleCustomer, uuid.Nil); len(got) != 0 {
		t.Errorf("customer replay = %v, want none", got)
	}
	if got := buf.Replay(manager, uuid.Nil); len(got) != 1 {
		t.Errorf("user-only replay = %d events, want 1", len(got))
	}

	now = now.Add(10 * time.Minute)
	buf.CleanExpired()
	if len(buf.roles) != 0 || len(buf.customers) != 0 {
		t.Errorf("CleanExpired left %d role and %d customer buffers", len(buf.roles), len(buf.customers))
	}
}
