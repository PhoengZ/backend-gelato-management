package service

import (
	"context"
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

type lifecycleRepo struct {
	checkoutRepo
	beginErr, finishErr error
	events              []domain.OutboxEvent
}

func (r *lifecycleRepo) Get(_ context.Context, id string) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order == nil || r.order.ID != id {
		return nil, repository.ErrNotFound
	}
	copy := *r.order
	return &copy, nil
}

func (r *lifecycleRepo) BeginTransition(_ context.Context, id string, target domain.OrderStatus) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	if r.order == nil || r.order.ID != id {
		return nil, repository.ErrNotFound
	}
	terminal := domain.StatusCancelled
	if target == "PAYMENT_CONFIRMING" {
		terminal = domain.StatusPaid
	}
	if r.order.Status != domain.StatusPendingPayment && r.order.Status != target && r.order.Status != terminal {
		return nil, repository.ErrInvalidTransition
	}
	if r.order.Status == domain.StatusPendingPayment {
		r.order.Status = target
	}
	copy := *r.order
	return &copy, nil
}

func (r *lifecycleRepo) PendingTransitions(_ context.Context, afterID string, _ int) ([]domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order != nil && r.order.ID > afterID && (r.order.Status == "CANCEL_PENDING" || r.order.Status == "PAYMENT_CONFIRMING") {
		return []domain.Order{*r.order}, nil
	}
	return nil, nil
}

func (r *lifecycleRepo) RejectPaymentConfirmation(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order.ID != id || r.order.Status != domain.StatusPaymentConfirming {
		return repository.ErrInvalidTransition
	}
	r.order.Status = domain.StatusCancelPending
	return nil
}

func (r *lifecycleRepo) MarkCancelled(_ context.Context, id string, event *domain.OutboxEvent) error {
	return r.finish(id, "CANCEL_PENDING", domain.StatusCancelled, event)
}

func (r *lifecycleRepo) MarkPaidWithOutbox(_ context.Context, id string, event *domain.OutboxEvent) error {
	return r.finish(id, "PAYMENT_CONFIRMING", domain.StatusPaid, event)
}

func (r *lifecycleRepo) finish(id string, from, to domain.OrderStatus, event *domain.OutboxEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finishErr != nil {
		return r.finishErr
	}
	if r.order.Status == to {
		return nil
	}
	if r.order.ID != id || r.order.Status != from {
		return repository.ErrInvalidTransition
	}
	r.order.Status = to
	r.events = append(r.events, *event)
	return nil
}

type lifecycleInventory struct {
	client.Inventory
	release func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error)
	confirm func(*inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error)
}

func (i *lifecycleInventory) ReleaseReservation(_ context.Context, req *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
	return i.release(req)
}
func (i *lifecycleInventory) ConfirmReservation(_ context.Context, req *inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
	return i.confirm(req)
}

func TestCancellationSaveFailureBlocksCheckoutReplay(t *testing.T) {
	r := &lifecycleRepo{}
	s, _ := NewOrderService(r, &checkoutInventory{}, &checkoutCatalog{}, &checkoutPayment{call: nil})
	input, key := checkoutInput(), uuid.NewString()
	order, err := s.(*orderService).placeOrder(context.Background(), input, key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.BindCheckout(context.Background(), order.ID, "cs_existing", sessionResponse().CheckoutUrl)
	if err != nil {
		t.Fatal(err)
	}
	i := &lifecycleInventory{release: func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		return &inventoryv1.ReleaseReservationResponse{Reservation: &inventoryv1.Reservation{Status: inventoryv1.ReservationStatus_RESERVATION_STATUS_RELEASED}}, nil
	}}
	s.(*orderService).inventory = i
	r.finishErr = errors.New("database unavailable after stock release")
	if err := s.CancelOrder(context.Background(), order.ID, "CUSTOMER_REQUEST"); err == nil {
		t.Fatal("expected save failure")
	}
	result, err := s.PlaceOrder(context.Background(), input, key)
	if err != nil {
		t.Fatal(err)
	}
	if result.CheckoutURL != "" {
		t.Fatal("checkout URL exposed after stock release")
	}
	if result.Status != "CANCEL_PENDING" {
		t.Fatalf("cancellation intent was not durable: %s", result.Status)
	}
}

func lifecycleOrder() *domain.Order {
	return &domain.Order{ID: uuid.NewString(), Status: domain.StatusPendingPayment,
		ReservationID: uuid.NewString(), ReleaseIdempotencyKey: uuid.NewString(),
		ConfirmIdempotencyKey: uuid.NewString(), ReservationExpiresAt: time.Now().Add(time.Hour)}
}

func TestCancellationClaimFailureNeverReleasesStock(t *testing.T) {
	r := &lifecycleRepo{beginErr: errors.New("database unavailable")}
	r.order = lifecycleOrder()
	i := &lifecycleInventory{release: func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		t.Fatal("stock released without durable cancellation intent")
		return nil, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.CancelOrder(context.Background(), r.order.ID, "CUSTOMER_REQUEST"); err == nil {
		t.Fatal("expected claim failure")
	}
	if r.order.Status != domain.StatusPendingPayment {
		t.Fatal("claim failure changed order")
	}
}

func TestCancellationWorkerResumesAfterRestartWithStableRequest(t *testing.T) {
	r := &lifecycleRepo{finishErr: errors.New("database unavailable after release")}
	r.order = lifecycleOrder()
	var requests []inventoryv1.ReleaseReservationRequest
	i := &lifecycleInventory{release: func(req *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		requests = append(requests, inventoryv1.ReleaseReservationRequest{OrderId: req.OrderId, ReservationId: req.ReservationId, IdempotencyKey: req.IdempotencyKey, Reason: req.Reason})
		if r.order.Status != domain.StatusCancelPending {
			t.Fatal("release started before cancellation intent")
		}
		return &inventoryv1.ReleaseReservationResponse{}, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.CancelOrder(context.Background(), r.order.ID, "PAYMENT_FAILED"); err == nil {
		t.Fatal("expected save failure")
	}
	r.finishErr = nil
	// A new service has no request-local knowledge of the original cancellation.
	restarted := &orderService{repo: r, inventory: i}
	if err := restarted.ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].Reason != "ORDER_CANCELLED" || requests[1].Reason != requests[0].Reason || requests[1].IdempotencyKey != requests[0].IdempotencyKey {
		t.Fatal("restart changed release facts")
	}
	if r.order.Status != domain.StatusCancelled || len(r.events) != 1 {
		t.Fatal("cancellation and outbox did not finish")
	}
	if err := restarted.CancelOrder(context.Background(), r.order.ID, "CUSTOMER_REQUEST"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(r.events) != 1 {
		t.Fatal("terminal cancellation was not idempotent")
	}
}

func TestCancellationTimeoutKeepsIntentForWorker(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = lifecycleOrder()
	attempts := 0
	i := &lifecycleInventory{release: func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
		attempts++
		if attempts == 1 {
			return nil, status.Error(codes.DeadlineExceeded, "ambiguous release")
		}
		return &inventoryv1.ReleaseReservationResponse{}, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.CancelOrder(context.Background(), r.order.ID, "CUSTOMER_REQUEST"); err == nil {
		t.Fatal("expected timeout")
	}
	if r.order.Status != domain.StatusCancelPending || len(r.events) != 0 {
		t.Fatal("timeout discarded cancellation intent")
	}
	if err := s.ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.order.Status != domain.StatusCancelled {
		t.Fatal("worker did not retry")
	}
}

func TestCancellationAndPaymentClaimOneWinner(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel_first", false: "payment_first"}[cancelFirst], func(t *testing.T) {
			r := &lifecycleRepo{}
			r.order = lifecycleOrder()
			entered, proceed := make(chan struct{}), make(chan struct{})
			i := &lifecycleInventory{}
			i.release = func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
				if !cancelFirst {
					t.Error("losing cancellation reached Inventory")
					return nil, errors.New("unexpected release")
				}
				close(entered)
				<-proceed
				return &inventoryv1.ReleaseReservationResponse{}, nil
			}
			i.confirm = func(*inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
				if cancelFirst {
					t.Error("losing payment reached Inventory")
					return nil, errors.New("unexpected confirmation")
				}
				close(entered)
				<-proceed
				return &inventoryv1.ConfirmReservationResponse{}, nil
			}
			s := &orderService{repo: r, inventory: i}
			done := make(chan error, 1)
			go func() {
				if cancelFirst {
					done <- s.CancelOrder(context.Background(), r.order.ID, "CUSTOMER_REQUEST")
				} else {
					done <- s.ConfirmPayment(context.Background(), r.order.ID)
				}
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("winning workflow did not reach Inventory")
			}
			var loser error
			if cancelFirst {
				loser = s.ConfirmPayment(context.Background(), r.order.ID)
			} else {
				loser = s.CancelOrder(context.Background(), r.order.ID, "PAYMENT_FAILED")
			}
			if appErr, ok := loser.(*AppError); !ok || appErr.HTTPStatus != 409 {
				t.Errorf("expected losing workflow conflict: %v", loser)
			}
			// A checkout response captured before the claim cannot bind afterward.
			if _, err := r.BindCheckout(context.Background(), r.order.ID, "cs_stale", sessionResponse().CheckoutUrl); !errors.Is(err, repository.ErrInvalidTransition) {
				t.Error("stale checkout binding accepted")
			}
			close(proceed)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if len(r.events) != 1 {
				t.Fatal("expected one terminal event")
			}
		})
	}
}

func TestPaymentWorkerFinishesConfirmedStockAfterSaveFailure(t *testing.T) {
	r := &lifecycleRepo{finishErr: errors.New("database unavailable after confirmation")}
	r.order = lifecycleOrder()
	var keys []string
	i := &lifecycleInventory{confirm: func(req *inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
		keys = append(keys, req.IdempotencyKey)
		return &inventoryv1.ConfirmReservationResponse{}, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.ConfirmPayment(context.Background(), r.order.ID); err == nil {
		t.Fatal("expected save failure")
	}
	if r.order.Status != domain.StatusPaymentConfirming {
		t.Fatal("payment intent was lost")
	}
	if err := s.CancelOrder(context.Background(), r.order.ID, "CUSTOMER_REQUEST"); err == nil {
		t.Fatal("cancellation took confirmed stock")
	}
	r.finishErr = nil
	if err := (&orderService{repo: r, inventory: i}).ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != keys[1] || r.order.Status != domain.StatusPaid || len(r.events) != 1 {
		t.Fatal("payment recovery was not idempotent")
	}
}

func TestPaymentTimeoutKeepsConfirmationClaim(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = lifecycleOrder()
	attempts := 0
	i := &lifecycleInventory{confirm: func(*inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
		attempts++
		if attempts == 1 {
			return nil, status.Error(codes.DeadlineExceeded, "confirmation may have committed")
		}
		return &inventoryv1.ConfirmReservationResponse{}, nil
	}}
	s := &orderService{repo: r, inventory: i}
	if err := s.ConfirmPayment(context.Background(), r.order.ID); err == nil {
		t.Fatal("expected timeout")
	}
	if r.order.Status != domain.StatusPaymentConfirming {
		t.Fatal("ambiguous confirmation reopened checkout")
	}
	if err := s.CancelOrder(context.Background(), r.order.ID, "CUSTOMER_REQUEST"); err == nil {
		t.Fatal("ambiguous confirmation allowed release")
	}
	if err := s.ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.order.Status != domain.StatusPaid || len(r.events) != 1 {
		t.Fatal("worker did not reconcile confirmation")
	}
}

func TestExpiredConfirmationEntersCancellationWithoutSale(t *testing.T) {
	r := &lifecycleRepo{}
	r.order = lifecycleOrder()
	i := &lifecycleInventory{
		confirm: func(*inventoryv1.ConfirmReservationRequest) (*inventoryv1.ConfirmReservationResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "reservation expired")
		},
		release: func(*inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
			return &inventoryv1.ReleaseReservationResponse{Reservation: &inventoryv1.Reservation{Status: inventoryv1.ReservationStatus_RESERVATION_STATUS_EXPIRED}}, nil
		},
	}
	s := &orderService{repo: r, inventory: i}
	if err := s.ConfirmPayment(context.Background(), r.order.ID); err == nil {
		t.Fatal("expired stock accepted")
	}
	if r.order.Status != domain.StatusCancelPending || len(r.events) != 0 {
		t.Fatal("expired stock left payable or recorded a sale")
	}
	if err := s.ResumePendingTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.order.Status != domain.StatusCancelled || len(r.events) != 1 || r.events[0].RoutingKey != "order.cancelled" {
		t.Fatal("expired payment was not cancelled without a sale")
	}
}

type pagedLifecycleRepo struct {
	repository.OrderRepository
	pages int
}

func (r *pagedLifecycleRepo) PendingTransitions(_ context.Context, afterID string, limit int) ([]domain.Order, error) {
	r.pages++
	if limit != 100 {
		return nil, errors.New("unexpected page size")
	}
	if afterID == "" {
		orders := make([]domain.Order, 100)
		for n := range orders {
			orders[n] = domain.Order{ID: uuid.NewString(), Status: domain.StatusCancelPending}
		}
		orders[99].ID = "00000000-0000-0000-0000-000000000100"
		return orders, nil
	}
	if afterID != "00000000-0000-0000-0000-000000000100" {
		return nil, errors.New("incorrect page cursor")
	}
	return []domain.Order{{ID: "00000000-0000-0000-0000-000000000101", Status: domain.StatusCancelPending}}, nil
}

func (r *pagedLifecycleRepo) BeginTransition(_ context.Context, _ string, _ domain.OrderStatus) (*domain.Order, error) {
	return nil, errors.New("individual order unavailable")
}

func TestLifecycleWorkerContinuesBeyondFailedFirstPage(t *testing.T) {
	r := &pagedLifecycleRepo{}
	s := &orderService{repo: r}
	if err := s.ResumePendingTransitions(context.Background()); err == nil {
		t.Fatal("expected recovery failures")
	}
	if r.pages != 2 {
		t.Fatal("failed orders starved later page")
	}
}
