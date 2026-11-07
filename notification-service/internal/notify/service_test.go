package notify

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/auth"
	"notification-service/internal/events"
	"notification-service/internal/hub"
)

type userPush struct {
	user uuid.UUID
	n    hub.Notification
}

type rolePush struct {
	roles []auth.Role
	n     hub.Notification
}

type fakePusher struct {
	users []userPush
	roles []rolePush
}

func (f *fakePusher) PushToUser(u uuid.UUID, n hub.Notification) int {
	f.users = append(f.users, userPush{u, n})
	return 1
}

func (f *fakePusher) PushToRoles(n hub.Notification, roles ...auth.Role) int {
	f.roles = append(f.roles, rolePush{roles, n})
	return 1
}

func env() events.Envelope {
	return events.Envelope{ID: uuid.New(), Time: time.Date(2026, 9, 2, 14, 55, 0, 0, time.UTC)}
}

func TestOrderPlacedRecordsOwnerWithoutPushing(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)
	orderID, customerID := uuid.New(), uuid.New()

	s.OrderPlaced(env(), events.OrderPlacedData{OrderID: orderID, CustomerID: customerID, TotalAmountMinor: 1250, Currency: "THB"})
	if len(p.users) != 0 {
		t.Fatalf("order.placed pushed %d notifications, want 0 (confirmation comes from fulfillment.order_queued)", len(p.users))
	}

	s.OrderReady(env(), events.OrderReadyData{OrderID: orderID, QueueNumber: "01"})
	if len(p.users) != 1 || p.users[0].user != customerID || p.users[0].n.Type != TypeOrderReady {
		t.Fatalf("pushes = %+v, want order.ready to the owner learned from order.placed", p.users)
	}
	if data := p.users[0].n.Data.(map[string]any); data["queue_number"] != "01" {
		t.Errorf("ready data = %v", data)
	}
}

func TestOrderQueuedConfirmsWithQueueNumber(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)
	e := env()
	d := events.OrderQueuedData{
		OrderID: uuid.New(), CustomerID: uuid.New(), QueueNumber: "03", PickupSlotID: uuid.New(),
		PickupAt: time.Date(2026, 9, 2, 22, 0, 0, 0, time.FixedZone("ICT", 7*3600)),
	}

	s.OrderQueued(e, d)
	s.OrderQueued(e, d) // at-least-once redelivery of the same event id

	if len(p.users) != 1 {
		t.Fatalf("pushes = %d, want 1", len(p.users))
	}
	push := p.users[0]
	if push.user != d.CustomerID {
		t.Errorf("sent to %s, want %s", push.user, d.CustomerID)
	}
	n := push.n
	if n.ID != e.ID || n.Type != TypeOrderConfirmed || n.Message != "Your gelato order is confirmed." || !n.OccurredAt.Equal(e.Time) {
		t.Errorf("notification = %+v", n)
	}
	data := n.Data.(map[string]any)
	want := map[string]any{
		"order_id": d.OrderID, "queue_number": "03", "pickup_slot_id": d.PickupSlotID,
		"pickup_at": time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC),
	}
	if len(data) != len(want) {
		t.Errorf("data = %v, want exactly %v", data, want)
	}
	for k, v := range want {
		if data[k] != v {
			t.Errorf("data.%s = %v, want %v", k, data[k], v)
		}
	}
}

func TestOrderCancelledRoutesToCustomer(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)
	orderID, customerID := uuid.New(), uuid.New()

	s.OrderPlaced(env(), events.OrderPlacedData{OrderID: orderID, CustomerID: customerID})
	s.OrderCancelled(env(), events.OrderCancelledData{OrderID: orderID, Reason: "PAYMENT_EXPIRED"})

	last := p.users[len(p.users)-1]
	if last.user != customerID || last.n.Type != TypeOrderCancelled {
		t.Errorf("got %s to %s", last.n.Type, last.user)
	}
	if last.n.Data.(map[string]any)["reason"] != "PAYMENT_EXPIRED" {
		t.Errorf("data = %v", last.n.Data)
	}
}

func TestUnknownOrderIsSkipped(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)

	s.OrderReady(env(), events.OrderReadyData{OrderID: uuid.New(), QueueNumber: "07"})
	s.OrderCancelled(env(), events.OrderCancelledData{OrderID: uuid.New()})

	if len(p.users) != 0 {
		t.Errorf("pushed %d notifications for unknown orders, want 0", len(p.users))
	}
}

func TestOwnerExpiresAfterTTL(t *testing.T) {
	p := &fakePusher{}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	s := newService(p, time.Hour, func() time.Time { return now })
	orderID := uuid.New()

	s.OrderPlaced(env(), events.OrderPlacedData{OrderID: orderID, CustomerID: uuid.New()})
	now = now.Add(2 * time.Hour)
	s.OrderReady(env(), events.OrderReadyData{OrderID: orderID})

	if len(p.users) != 0 {
		t.Errorf("pushes = %d, want 0 once the owner has expired", len(p.users))
	}
}

func TestDuplicateEventIsNotPushedTwice(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)
	e := env()
	d := events.OrderReadyData{OrderID: uuid.New(), CustomerID: uuid.New(), QueueNumber: "02"}

	s.OrderReady(e, d)
	s.OrderReady(e, d) // at-least-once redelivery of the same event id

	if len(p.users) != 1 {
		t.Errorf("pushes = %d, want 1", len(p.users))
	}
}

func TestInventoryAlertsGoToStaffAndManagers(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)

	s.BatchLowStock(env(), events.BatchLowStockData{BatchID: uuid.New(), RemainingPortions: 2, ThresholdPortions: 5})
	s.BatchExpiring(env(), events.BatchExpiringData{BatchID: uuid.New(), ExpiresAt: time.Now()})

	if len(p.roles) != 2 || len(p.users) != 0 {
		t.Fatalf("role pushes = %d, user pushes = %d", len(p.roles), len(p.users))
	}
	for _, push := range p.roles {
		if len(push.roles) != 2 || push.roles[0] != auth.RoleStaff || push.roles[1] != auth.RoleManager {
			t.Errorf("roles = %v, want [STAFF MANAGER]", push.roles)
		}
	}
}

func TestSeenSetEvictsOldestBeyondCapacity(t *testing.T) {
	set := newSeenSet(2)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	set.markNew(a)
	set.markNew(b)
	set.markNew(c) // evicts a

	if !set.markNew(a) {
		t.Error("evicted id should be accepted again")
	}
	if set.markNew(c) {
		t.Error("recent id should still be remembered")
	}
}

func TestOwnerMapEvictsOldestBeyondCapacity(t *testing.T) {
	m := newOwnerMap(time.Hour, 2, time.Now)
	o1, o2, o3 := uuid.New(), uuid.New(), uuid.New()
	m.put(o1, uuid.New())
	m.put(o2, uuid.New())
	m.put(o3, uuid.New())

	if _, ok := m.get(o1); ok {
		t.Error("oldest order should have been evicted")
	}
	if _, ok := m.get(o3); !ok {
		t.Error("newest order should be kept")
	}
}

func TestOrderEventsRouteByCustomerIDWithoutOrderPlaced(t *testing.T) {
	p := &fakePusher{}
	s := New(p, time.Hour)
	customer := uuid.New()

	s.OrderCancelled(env(), events.OrderCancelledData{OrderID: uuid.New(), CustomerID: customer})
	s.OrderReady(env(), events.OrderReadyData{OrderID: uuid.New(), QueueNumber: "07", CustomerID: customer})

	if len(p.users) != 2 || p.users[0].user != customer || p.users[1].user != customer {
		t.Fatalf("pushed to %v, want the event customer twice", p.users)
	}
}
