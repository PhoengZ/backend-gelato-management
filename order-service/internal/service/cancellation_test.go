package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	inventoryv1 "order-service/gen/inventory/v1"
	"order-service/internal/client"
	"order-service/internal/domain"
	"order-service/internal/repository"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *lifecycleRepo) ExpiredPendingOrders(_ context.Context, afterID string, now time.Time, _ int) ([]domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order != nil && r.order.ID > afterID && r.order.Status == domain.StatusPendingPayment && !r.order.ReservationExpiresAt.After(now) {
		return []domain.Order{*r.order}, nil
	}
	return nil, nil
}

func (r *lifecycleRepo) BeginExpiry(_ context.Context, id string, now time.Time) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	if r.order == nil || r.order.ID != id {
		return nil, repository.ErrNotFound
	}
	if r.order.Status != domain.StatusPendingPayment || r.order.ReservationExpiresAt.After(now) {
		return nil, repository.ErrInvalidTransition
	}
	r.order.Status, r.order.CancellationReason = domain.StatusCancelPending, domain.ReasonReservationExpired
	copy := *r.order
	return &copy, nil
}

func (r *lifecycleRepo) BeginStoreCancellation(_ context.Context, id string) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	if r.order == nil || r.order.ID != id {
		return nil, repository.ErrNotFound
	}
	switch {
	case r.order.Status == domain.StatusStoreCancelPending,
		r.order.Status == domain.StatusCancelled && r.order.CancellationReason == domain.ReasonStoreCancelled:
	case r.order.Status == domain.StatusPaid:
		r.order.Status, r.order.CancellationReason = domain.StatusStoreCancelPending, domain.ReasonStoreCancelled
	default:
		return nil, repository.ErrInvalidTransition
	}
	copy := *r.order
	return &copy, nil
}

type returningInventory struct {
	lifecycleInventory
	returns func(*inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error)
}

func (i *returningInventory) ReturnConfirmedReservation(_ context.Context, req *inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error) {
	return i.returns(req)
}

type ticketFulfillment struct {
	client.Fulfillment
	ticket  *client.Ticket
	err     error
	calls   int
	bearers []string
}

func (f *ticketFulfillment) GetTicket(ctx context.Context, orderID string) (*client.Ticket, error) {
	f.calls++
	if bearer, ok := client.UserAuthorization(ctx); ok {
		f.bearers = append(f.bearers, bearer)
	}
	return f.ticket, f.err
}

type cancelledEvent struct {
	Type string `json:"type"`
	Data struct {
		OrderID          string             `json:"order_id"`
		CustomerID       string             `json:"customer_id"`
		Reason           string             `json:"reason"`
		WasPaid          *bool              `json:"was_paid"`
		TotalAmountMinor *int64             `json:"total_amount_minor"`
		Currency         *string            `json:"currency"`
		Items            []domain.OrderItem `json:"items"`
		PaidAt           *time.Time         `json:"paid_at"`
	} `json:"data"`
}

func decodeCancelled(t *testing.T, event domain.OutboxEvent) cancelledEvent {
	t.Helper()
	if event.RoutingKey != "order.cancelled" {
		t.Fatalf("routing key = %s", event.RoutingKey)
	}
	var out cancelledEvent
	if err := json.Unmarshal(event.Payload, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertAppError(t *testing.T, err error, httpStatus int, code string) {
	t.Helper()
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.HTTPStatus != httpStatus || appErr.Code != code {
		t.Fatalf("error = %#v, want %d %s", err, httpStatus, code)
	}
}

func overdueOrder() *domain.Order {
	order := lifecycleOrder()
	order.CustomerID = uuid.NewString()
	order.ReservationExpiresAt = time.Now().UTC().Add(-time.Minute)
	return order
}

func TestExpiryWorkerCancelsOverdueOrderAndReleasesStock(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = overdueOrder()
	var released []*inventoryv1.ReleaseReservationRequest
	i := &lifecycleInventory{release: func(req *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		if r.order.Status != domain.StatusCancelPending {
			t.Fatal("stock released before the expiry intent was durable")
		}
		released = append(released, req)
		return &inventoryv1.ReleaseReservationResponse{Reservation: &inventoryv1.Reservation{Status: inventoryv1.ReservationStatus_RESERVATION_STATUS_EXPIRED}}, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.ExpireOverdueOrders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || released[0].IdempotencyKey != r.order.ReleaseIdempotencyKey || released[0].ReservationId != r.order.ReservationID || released[0].Reason != domain.ReasonReservationExpired {
		t.Fatalf("release request = %+v", released)
	}
	if r.order.Status != domain.StatusCancelled || len(r.events) != 1 {
		t.Fatal("overdue order not cancelled with an event")
	}
	event := decodeCancelled(t, r.events[0])
	if event.Data.Reason != domain.ReasonReservationExpired || event.Data.CustomerID != r.order.CustomerID || event.Data.WasPaid != nil || event.Data.TotalAmountMinor != nil || event.Data.Items != nil {
		t.Fatalf("unexpected cancellation event %+v", event.Data)
	}
	// A payment arriving after expiry is rejected so Payment refunds it.
	assertPaymentRejected(t, s.ConfirmPayment(context.Background(), r.order.ID))
}

func TestExpiryWorkerNeverCancelsAConfirmingPaymentOrUnexpiredHold(t *testing.T) {
	for _, order := range []*domain.Order{
		func() *domain.Order { o := overdueOrder(); o.Status = domain.StatusPaymentConfirming; return o }(),
		func() *domain.Order {
			o := overdueOrder()
			o.ReservationExpiresAt = time.Now().Add(time.Hour)
			return o
		}(),
	} {
		r := &lifecycleRepo{}
		r.order = order
		before := order.Status
		i := &lifecycleInventory{release: func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
			t.Fatal("expiry released stock it did not claim")
			return nil, nil
		}}
		s := &orderService{repo: r, inventory: i}
		if err := s.ExpireOverdueOrders(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r.order.Status != before || len(r.events) != 0 {
			t.Fatalf("status = %s, events = %d", r.order.Status, len(r.events))
		}
	}
}

// racingRepo reports an order as overdue, but a payment claims it before the
// expiry claim takes the row lock.
type racingRepo struct{ lifecycleRepo }

func (r *racingRepo) ExpiredPendingOrders(ctx context.Context, afterID string, now time.Time, limit int) ([]domain.Order, error) {
	orders, err := r.lifecycleRepo.ExpiredPendingOrders(ctx, afterID, now, limit)
	r.mu.Lock()
	r.order.Status = domain.StatusPaymentConfirming
	r.mu.Unlock()
	return orders, err
}

func TestExpiryLosesToPaymentClaimedAfterDiscovery(t *testing.T) {
	r := &racingRepo{}
	r.order = overdueOrder()
	i := &lifecycleInventory{release: func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		t.Fatal("expiry released a reservation that payment claimed")
		return nil, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.ExpireOverdueOrders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.order.Status != domain.StatusPaymentConfirming {
		t.Fatalf("status = %s", r.order.Status)
	}
}

func TestExpiryIntentSurvivesRestartWithItsReason(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = overdueOrder()
	var reasons, keys []string
	attempts := 0
	i := &lifecycleInventory{release: func(req *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		attempts++
		reasons, keys = append(reasons, req.Reason), append(keys, req.IdempotencyKey)
		if attempts == 1 {
			return nil, status.Error(codes.Unavailable, "inventory down")
		}
		return &inventoryv1.ReleaseReservationResponse{}, nil
	}}
	if err := (&orderService{repo: r, inventory: i}).ExpireOverdueOrders(context.Background()); err == nil {
		t.Fatal("expected release failure to be reported")
	}
	if r.order.Status != domain.StatusCancelPending {
		t.Fatalf("expiry intent lost: %s", r.order.Status)
	}
	restarted := &orderService{repo: r, inventory: i}
	if err := restarted.ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != keys[1] || reasons[0] != reasons[1] || reasons[1] != domain.ReasonReservationExpired {
		t.Fatalf("restart changed release facts: %v %v", reasons, keys)
	}
	if r.order.Status != domain.StatusCancelled || decodeCancelled(t, r.events[0]).Data.Reason != domain.ReasonReservationExpired {
		t.Fatal("restarted expiry did not finish with RESERVATION_EXPIRED")
	}
}

type pagedExpiryRepo struct {
	repository.OrderRepository
	pages   []string
	claimed int
}

func (r *pagedExpiryRepo) ExpiredPendingOrders(_ context.Context, afterID string, _ time.Time, limit int) ([]domain.Order, error) {
	r.pages = append(r.pages, afterID)
	if afterID == "" {
		orders := make([]domain.Order, limit)
		for n := range orders {
			orders[n] = domain.Order{ID: uuid.NewString(), Status: domain.StatusPendingPayment}
		}
		orders[limit-1].ID = "00000000-0000-0000-0000-000000000100"
		return orders, nil
	}
	if afterID != "00000000-0000-0000-0000-000000000100" {
		return nil, errors.New("incorrect page cursor")
	}
	return []domain.Order{{ID: "00000000-0000-0000-0000-000000000101", Status: domain.StatusPendingPayment}}, nil
}

func (r *pagedExpiryRepo) BeginExpiry(context.Context, string, time.Time) (*domain.Order, error) {
	r.claimed++
	return nil, errors.New("order row unavailable")
}

func TestExpiryWorkerPagesPastFailedOrders(t *testing.T) {
	r := &pagedExpiryRepo{}
	s := &orderService{repo: r}
	if err := s.ExpireOverdueOrders(context.Background()); err == nil {
		t.Fatal("expected failures to be reported")
	}
	if len(r.pages) != 2 || r.claimed != expiryPageSize+1 {
		t.Fatalf("pages = %v, claims = %d", r.pages, r.claimed)
	}
}

func paidOrder() *domain.Order {
	order := lifecycleOrder()
	order.CustomerID = uuid.NewString()
	order.Status = domain.StatusPaid
	paidAt := time.Date(2026, 9, 2, 14, 30, 0, 0, time.UTC)
	order.PaidAt = &paidAt
	order.TotalAmountMinor, order.Currency = 1250, "THB"
	order.Items = []domain.OrderItem{{FlavorID: uuid.NewString(), FlavorName: "Pistachio", Portions: 2, UnitPriceMinor: 625, SubtotalMinor: 1250}}
	return order
}

func storeCancelService(r *lifecycleRepo, f client.Fulfillment, returns func(*inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error)) *orderService {
	return &orderService{repo: r, inventory: &returningInventory{returns: returns}, fulfillment: f}
}

func TestStoreCancelReturnsStockAndAnnouncesPaidFacts(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = paidOrder()
	f := &ticketFulfillment{ticket: &client.Ticket{OrderID: r.order.ID, Status: "QUEUED", QueueNumber: "03"}}
	var requests []*inventoryv1.ReturnConfirmedReservationRequest
	s := storeCancelService(r, f, func(req *inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error) {
		if r.order.Status != domain.StatusStoreCancelPending {
			t.Fatal("stock returned before the store-cancel intent was durable")
		}
		requests = append(requests, req)
		return &inventoryv1.ReturnConfirmedReservationResponse{Reservation: &inventoryv1.Reservation{Status: inventoryv1.ReservationStatus_RESERVATION_STATUS_RETURNED}}, nil
	})
	ctx := client.WithUserAuthorization(context.Background(), "Bearer staff")
	if err := s.StoreCancelOrder(ctx, r.order.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.bearers) != 1 || f.bearers[0] != "Bearer staff" {
		t.Fatalf("caller bearer not forwarded: %v", f.bearers)
	}
	wantKey := uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.order.ID+"/return")).String()
	if len(requests) != 1 || requests[0].ReservationId != r.order.ReservationID || requests[0].OrderId != r.order.ID || requests[0].IdempotencyKey != wantKey || requests[0].Reason != domain.ReasonStoreCancelled {
		t.Fatalf("return request = %+v", requests)
	}
	if r.order.Status != domain.StatusCancelled || len(r.events) != 1 {
		t.Fatal("store cancellation did not finish")
	}
	event := decodeCancelled(t, r.events[0]).Data
	if event.Reason != domain.ReasonStoreCancelled || event.WasPaid == nil || !*event.WasPaid || event.TotalAmountMinor == nil || *event.TotalAmountMinor != 1250 ||
		event.Currency == nil || *event.Currency != "THB" || len(event.Items) != 1 || event.Items[0].SubtotalMinor != 1250 || event.PaidAt == nil || !event.PaidAt.Equal(*r.order.PaidAt) || event.CustomerID != r.order.CustomerID {
		t.Fatalf("paid cancellation facts missing: %+v", event)
	}
	if err := s.StoreCancelOrder(ctx, r.order.ID); err != nil || len(requests) != 1 || len(r.events) != 1 {
		t.Fatal("repeated store cancellation was not a no-op", err)
	}
	// A duplicate payment callback must not trigger an automatic refund.
	assertAppError(t, s.ConfirmPayment(context.Background(), r.order.ID), 409, "ORDER_STATE_CONFLICT")
}

func TestStoreCancelRequiresAQueuedTicket(t *testing.T) {
	for _, tc := range []struct {
		name   string
		f      *ticketFulfillment
		status int
		code   string
	}{
		{"preparing", &ticketFulfillment{ticket: &client.Ticket{Status: "PREPARING"}}, 409, "ORDER_PREPARATION_STARTED"},
		{"ready", &ticketFulfillment{ticket: &client.Ticket{Status: "READY_FOR_PICKUP"}}, 409, "ORDER_PREPARATION_STARTED"},
		{"missing", &ticketFulfillment{err: client.ErrTicketNotFound}, 409, "ORDER_PREPARATION_STARTED"},
		{"unavailable", &ticketFulfillment{err: errors.New("connection refused")}, 503, "FULFILLMENT_UNAVAILABLE"},
		{"credentials rejected", &ticketFulfillment{err: client.ErrTicketForbidden}, 403, "FORBIDDEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &lifecycleRepo{}
			r.order = paidOrder()
			s := storeCancelService(r, tc.f, func(*inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error) {
				t.Fatal("stock returned without a queued ticket")
				return nil, nil
			})
			assertAppError(t, s.StoreCancelOrder(context.Background(), r.order.ID), tc.status, tc.code)
			if r.order.Status != domain.StatusPaid || len(r.events) != 0 {
				t.Fatalf("order changed: %s", r.order.Status)
			}
		})
	}
}

func TestStoreCancelWithoutFulfillmentClientIsUnavailable(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = paidOrder()
	s := &orderService{repo: r, inventory: &returningInventory{}}
	assertAppError(t, s.StoreCancelOrder(context.Background(), r.order.ID), 503, "FULFILLMENT_UNAVAILABLE")
}

func TestStoreCancelOnlyAppliesToPaidOrders(t *testing.T) {
	for _, current := range []domain.OrderStatus{domain.StatusPendingPayment, domain.StatusPaymentConfirming, domain.StatusCancelPending, domain.StatusCompleted} {
		r := &lifecycleRepo{}
		r.order = paidOrder()
		r.order.Status = current
		f := &ticketFulfillment{ticket: &client.Ticket{Status: "QUEUED"}}
		s := storeCancelService(r, f, nil)
		assertAppError(t, s.StoreCancelOrder(context.Background(), r.order.ID), 409, "ORDER_STATE_CONFLICT")
		if f.calls != 0 {
			t.Fatalf("%s: Fulfillment called for a non-paid order", current)
		}
	}
	s := storeCancelService(&lifecycleRepo{}, &ticketFulfillment{}, nil)
	assertAppError(t, s.StoreCancelOrder(context.Background(), "not-a-uuid"), 400, "INVALID_ARGUMENT")
	assertAppError(t, s.StoreCancelOrder(context.Background(), uuid.NewString()), 404, "ORDER_NOT_FOUND")
}

func TestStoreCancelIntentSurvivesRestart(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = paidOrder()
	f := &ticketFulfillment{ticket: &client.Ticket{Status: "QUEUED"}}
	var keys []string
	attempts := 0
	returns := func(req *inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error) {
		attempts++
		keys = append(keys, req.IdempotencyKey)
		if attempts == 1 {
			return nil, status.Error(codes.DeadlineExceeded, "return may have committed")
		}
		return &inventoryv1.ReturnConfirmedReservationResponse{}, nil
	}
	if err := storeCancelService(r, f, returns).StoreCancelOrder(context.Background(), r.order.ID); err == nil {
		t.Fatal("expected Inventory timeout")
	}
	if r.order.Status != domain.StatusStoreCancelPending || len(r.events) != 0 {
		t.Fatalf("store-cancel intent lost: %s", r.order.Status)
	}
	// Preparation may start after the intent committed; recovery must not
	// re-check Fulfillment or the intent could never finish.
	f.ticket = &client.Ticket{Status: "PREPARING"}
	restarted := storeCancelService(r, f, returns)
	if err := restarted.ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || len(keys) != 2 || keys[0] != keys[1] {
		t.Fatalf("recovery changed facts: calls=%d keys=%v", f.calls, keys)
	}
	if r.order.Status != domain.StatusCancelled || len(r.events) != 1 || decodeCancelled(t, r.events[0]).Data.Reason != domain.ReasonStoreCancelled {
		t.Fatal("recovered store cancellation did not finish")
	}
}

func TestStoreCancelOfLegacyPaidOrderFallsBackToUpdatedAt(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = paidOrder()
	r.order.PaidAt = nil
	r.order.UpdatedAt = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	s := storeCancelService(r, &ticketFulfillment{ticket: &client.Ticket{Status: "QUEUED"}}, func(*inventoryv1.ReturnConfirmedReservationRequest) (*inventoryv1.ReturnConfirmedReservationResponse, error) {
		return &inventoryv1.ReturnConfirmedReservationResponse{}, nil
	})
	if err := s.StoreCancelOrder(context.Background(), r.order.ID); err != nil {
		t.Fatal(err)
	}
	if paidAt := decodeCancelled(t, r.events[0]).Data.PaidAt; paidAt == nil || !paidAt.Equal(r.order.UpdatedAt) {
		t.Fatalf("paid_at = %v", paidAt)
	}
}

func TestPaymentRecordsPaidAtEqualToOrderPlacedTime(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = lifecycleOrder()
	i := &lifecycleInventory{confirm: func(*inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
		return &inventoryv1.ConfirmReservationResponse{}, nil
	}}
	if err := (&orderService{repo: r, inventory: i}).ConfirmPayment(context.Background(), r.order.ID); err != nil {
		t.Fatal(err)
	}
	var placed struct {
		Time time.Time `json:"time"`
	}
	if err := json.Unmarshal(r.events[0].Payload, &placed); err != nil {
		t.Fatal(err)
	}
	if r.order.PaidAt == nil || !r.order.PaidAt.Equal(placed.Time) {
		t.Fatalf("paid_at %v differs from order.placed time %v", r.order.PaidAt, placed.Time)
	}
}
