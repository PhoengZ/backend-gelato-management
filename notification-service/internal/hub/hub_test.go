package hub

import (
	"testing"

	"github.com/google/uuid"

	"notification-service/internal/auth"
)

func note(kind string) Notification { return Notification{ID: uuid.New(), Type: kind} }

func drain(s *Subscriber) []string {
	var got []string
	for {
		select {
		case n := <-s.C():
			got = append(got, n.Type)
		default:
			return got
		}
	}
}

func TestPushToUserReachesOnlyThatUser(t *testing.T) {
	h := New(4)
	alice, bob := uuid.New(), uuid.New()
	a1, cancelA1 := h.Subscribe(alice, auth.RoleCustomer)
	a2, cancelA2 := h.Subscribe(alice, auth.RoleCustomer) // second tab
	b, cancelB := h.Subscribe(bob, auth.RoleCustomer)
	defer cancelA1()
	defer cancelA2()
	defer cancelB()

	if got := h.PushToUser(alice, note("order.ready")); got != 2 {
		t.Fatalf("delivered to %d connections, want 2", got)
	}
	if len(drain(a1)) != 1 || len(drain(a2)) != 1 {
		t.Error("both of alice's connections should receive the notification")
	}
	if len(drain(b)) != 0 {
		t.Error("bob must not receive alice's notification")
	}
}

func TestPushToRolesReachesStaffAndManagerOnly(t *testing.T) {
	h := New(4)
	customer, cancelC := h.Subscribe(uuid.New(), auth.RoleCustomer)
	staff, cancelS := h.Subscribe(uuid.New(), auth.RoleStaff)
	manager, cancelM := h.Subscribe(uuid.New(), auth.RoleManager)
	defer cancelC()
	defer cancelS()
	defer cancelM()

	if got := h.PushToRoles(note("inventory.low_stock"), auth.RoleStaff, auth.RoleManager); got != 2 {
		t.Fatalf("delivered to %d, want 2", got)
	}
	if len(drain(staff)) != 1 || len(drain(manager)) != 1 {
		t.Error("staff and manager should receive the alert")
	}
	if len(drain(customer)) != 0 {
		t.Error("customers must not receive inventory alerts")
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	h := New(4)
	user := uuid.New()
	_, cancel := h.Subscribe(user, auth.RoleCustomer)
	cancel()

	if got := h.PushToUser(user, note("order.ready")); got != 0 {
		t.Errorf("delivered to %d connections after cancel, want 0", got)
	}
}

func TestSlowSubscriberDropsOldestWithoutBlocking(t *testing.T) {
	h := New(2)
	user := uuid.New()
	s, cancel := h.Subscribe(user, auth.RoleCustomer)
	defer cancel()

	for _, kind := range []string{"first", "second", "third"} {
		h.PushToUser(user, note(kind)) // must not block even though nobody reads
	}
	got := drain(s)
	if len(got) != 2 || got[0] != "second" || got[1] != "third" {
		t.Errorf("buffered = %v, want [second third]", got)
	}
}
