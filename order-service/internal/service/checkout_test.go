package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	catalogv1 "order-service/gen/catalog/v1"
	inventoryv1 "order-service/gen/inventory/v1"
	paymentv1 "order-service/gen/payment/v1"
	"order-service/internal/client"
	"order-service/internal/domain"
	"order-service/internal/repository"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type checkoutRepo struct {
	repository.OrderRepository
	mu        sync.Mutex
	order     *domain.Order
	createErr error
	bindErr   error
	creates   int
}

func (r *checkoutRepo) GetByIdempotencyKey(_ context.Context, customerID, key string) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.order == nil || r.order.CustomerID != customerID || r.order.IdempotencyKey != key {
		return nil, repository.ErrNotFound
	}
	copy := *r.order
	return &copy, nil
}
func (r *checkoutRepo) Create(_ context.Context, order *domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.creates++
	if r.createErr != nil {
		return r.createErr
	}
	copy := *order
	r.order = &copy
	return nil
}
func (r *checkoutRepo) BindCheckout(_ context.Context, id, session, url string) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bindErr != nil {
		return nil, r.bindErr
	}
	if r.order == nil || r.order.ID != id {
		return nil, repository.ErrNotFound
	}
	if r.order.Status != domain.StatusPendingPayment || !r.order.ReservationExpiresAt.After(time.Now()) {
		return nil, repository.ErrInvalidTransition
	}
	if r.order.CheckoutSessionID != "" && r.order.CheckoutSessionID != session {
		return nil, repository.ErrInvalidTransition
	}
	r.order.CheckoutSessionID, r.order.CheckoutURL = session, url
	copy := *r.order
	return &copy, nil
}

type checkoutInventory struct {
	client.Inventory
	reserves, releases int
}

func (i *checkoutInventory) ReservePortions(_ context.Context, req *inventoryv1.ReservePortionsRequest) (*inventoryv1.ReservePortionsResponse, error) {
	i.reserves++
	return &inventoryv1.ReservePortionsResponse{Reservation: &inventoryv1.Reservation{
		ReservationId: uuid.NewString(), OrderId: req.OrderId,
		Status:    inventoryv1.ReservationStatus_RESERVATION_STATUS_ACTIVE,
		ExpiresAt: timestamppb.New(time.Now().Add(10 * time.Minute)),
	}}, nil
}
func (i *checkoutInventory) ReleaseReservation(context.Context, *inventoryv1.ReleaseReservationRequest) (*inventoryv1.ReleaseReservationResponse, error) {
	i.releases++
	return &inventoryv1.ReleaseReservationResponse{}, nil
}

type checkoutCatalog struct{ client.Catalog }

func (c *checkoutCatalog) BatchGetFlavors(_ context.Context, ids []string) ([]*catalogv1.Flavor, error) {
	amount, currency := int64(7500), "THB"
	return []*catalogv1.Flavor{{Id: ids[0], Name: "Vanilla", Active: true, Price: &catalogv1.Money{AmountMinor: &amount, Currency: &currency}}}, nil
}

type checkoutPayment struct {
	call func(*paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error)
}

func (p *checkoutPayment) CreateCheckoutSession(_ context.Context, req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
	return p.call(req)
}
func sessionResponse() *paymentv1.CreateCheckoutSessionResponse {
	return &paymentv1.CreateCheckoutSessionResponse{CheckoutSessionId: "cs_test_checkout", CheckoutUrl: "https://checkout.stripe.com/c/pay/test"}
}
func checkoutInput() CreateOrderInput {
	return CreateOrderInput{CustomerID: uuid.NewString(), PickupAt: time.Now().UTC().Add(time.Hour), Items: []OrderItemInput{{FlavorID: uuid.NewString(), Portions: 2}}}
}

func TestCheckoutPaymentTimeoutRetryReusesOrderAndReservation(t *testing.T) {
	r := &checkoutRepo{}
	i := &checkoutInventory{}
	var keys []string
	p := &checkoutPayment{call: func(req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		if r.order == nil {
			t.Fatal("Payment called before Order was persisted")
		}
		if req.AmountMinor != 15000 || req.Currency != "THB" || req.CustomerId != r.order.CustomerID {
			t.Fatal("Payment did not receive authoritative checkout facts")
		}
		keys = append(keys, req.IdempotencyKey)
		if len(keys) == 1 {
			return nil, status.Error(codes.DeadlineExceeded, "ambiguous result")
		}
		return sessionResponse(), nil
	}}
	s, _ := NewOrderService(r, i, &checkoutCatalog{}, p)
	input, key := checkoutInput(), uuid.NewString()
	_, err := s.PlaceOrder(context.Background(), input, key)
	appErr, ok := err.(*AppError)
	if !ok || appErr.HTTPStatus != 504 || appErr.Details["order_id"] != r.order.ID {
		t.Fatalf("expected recoverable payment timeout: %v", err)
	}
	result, err := s.PlaceOrder(context.Background(), input, key)
	if err != nil || result.CheckoutURL != sessionResponse().CheckoutUrl {
		t.Fatalf("retry: %v", err)
	}
	if keys[0] != keys[1] || r.creates != 1 || i.reserves != 1 || i.releases != 0 {
		t.Fatal("retry duplicated checkout or released its hold")
	}
	_, err = s.PlaceOrder(context.Background(), input, key)
	if err != nil || len(keys) != 2 {
		t.Fatal("bound session should be reused without another Payment call")
	}
}

func TestCheckoutBindingFailureDoesNotExposeURLAndRetriesSamePayment(t *testing.T) {
	r := &checkoutRepo{bindErr: errors.New("database unavailable")}
	i := &checkoutInventory{}
	var keys []string
	p := &checkoutPayment{call: func(req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		keys = append(keys, req.IdempotencyKey)
		return sessionResponse(), nil
	}}
	s, _ := NewOrderService(r, i, &checkoutCatalog{}, p)
	input, key := checkoutInput(), uuid.NewString()
	result, err := s.PlaceOrder(context.Background(), input, key)
	if err == nil || result != nil {
		t.Fatal("uncommitted URL was exposed")
	}
	r.bindErr = nil
	result, err = s.PlaceOrder(context.Background(), input, key)
	if err != nil || result.CheckoutURL == "" || keys[0] != keys[1] || i.reserves != 1 {
		t.Fatalf("retry after ambiguous binding: %v", err)
	}
}

func TestCheckoutRejectsChangedRequestOnRetry(t *testing.T) {
	r, i := &checkoutRepo{}, &checkoutInventory{}
	calls := 0
	p := &checkoutPayment{call: func(*paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		calls++
		return sessionResponse(), nil
	}}
	s, _ := NewOrderService(r, i, &checkoutCatalog{}, p)
	input, key := checkoutInput(), uuid.NewString()
	if _, err := s.PlaceOrder(context.Background(), input, key); err != nil {
		t.Fatal(err)
	}
	input.Items[0].Portions++
	_, err := s.PlaceOrder(context.Background(), input, key)
	if appErr, ok := err.(*AppError); !ok || appErr.Code != "IDEMPOTENCY_KEY_REUSED" || calls != 1 {
		t.Fatalf("changed request: %v", err)
	}
}

func TestCheckoutSaveFailureNeverStartsPaymentOrReleasesConcurrentHold(t *testing.T) {
	r, i := &checkoutRepo{createErr: errors.New("write failed")}, &checkoutInventory{}
	p := &checkoutPayment{call: func(*paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		t.Fatal("payment started without a durable order")
		return nil, nil
	}}
	s, _ := NewOrderService(r, i, &checkoutCatalog{}, p)
	_, err := s.PlaceOrder(context.Background(), checkoutInput(), uuid.NewString())
	if err == nil || i.releases != 0 {
		t.Fatal("save failure must leave hold bounded by Inventory TTL")
	}
}

func TestCheckoutNeverExposesInvalidProviderURL(t *testing.T) {
	for _, address := range []string{"", "http://checkout.stripe.com/pay", "javascript:alert(1)", "https://user:pass@example.com/pay"} {
		t.Run(address, func(t *testing.T) {
			r, i := &checkoutRepo{}, &checkoutInventory{}
			p := &checkoutPayment{call: func(*paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
				resp := sessionResponse()
				resp.CheckoutUrl = address
				return resp, nil
			}}
			s, _ := NewOrderService(r, i, &checkoutCatalog{}, p)
			result, err := s.PlaceOrder(context.Background(), checkoutInput(), uuid.NewString())
			if err == nil || result != nil || r.order.CheckoutSessionID != "" {
				t.Fatal("invalid URL exposed or persisted")
			}
		})
	}
}

func TestCheckoutPaidOrExpiredReplayDoesNotStartPayment(t *testing.T) {
	for _, state := range []domain.OrderStatus{domain.StatusPaid, domain.StatusCancelled, domain.StatusPendingPayment} {
		t.Run(string(state), func(t *testing.T) {
			p := &checkoutPayment{call: func(*paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
				t.Fatal("terminal or expired order started payment")
				return nil, nil
			}}
			s := &orderService{payment: p}
			result, err := s.startPayment(context.Background(), &domain.Order{Status: state, ReservationExpiresAt: time.Now().Add(-time.Minute), CheckoutURL: sessionResponse().CheckoutUrl})
			if state == domain.StatusPendingPayment {
				if err == nil {
					t.Fatal("expired hold accepted")
				}
			} else if err != nil || result.CheckoutURL != "" {
				t.Fatal("terminal replay exposed payment URL")
			}
		})
	}
}

func TestCheckoutCancellationWhilePaymentRunsPreventsURLExposure(t *testing.T) {
	r, i := &checkoutRepo{}, &checkoutInventory{}
	p := &checkoutPayment{call: func(*paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
		r.order.Status = domain.StatusCancelled
		return sessionResponse(), nil
	}}
	s, _ := NewOrderService(r, i, &checkoutCatalog{}, p)
	result, err := s.PlaceOrder(context.Background(), checkoutInput(), uuid.NewString())
	if appErr, ok := err.(*AppError); !ok || appErr.Code != "CHECKOUT_STATE_CONFLICT" || result != nil {
		t.Fatalf("cancel race: %v", err)
	}
}
