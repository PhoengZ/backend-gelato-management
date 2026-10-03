package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	catalogv1 "order-service/gen/catalog/v1"
	inventoryv1 "order-service/gen/inventory/v1"
	"order-service/internal/client"
	"order-service/internal/domain"
	"order-service/internal/repository"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AppError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *AppError) Error() string { return e.Message }

type CreateOrderInput struct {
	CustomerID string           `json:"-"`
	PickupAt   time.Time        `json:"pickup_at"`
	Items      []OrderItemInput `json:"items"`
}
type OrderItemInput struct {
	FlavorID string `json:"flavor_id"`
	Portions int32  `json:"portions"`
}

type Service interface {
	PlaceOrder(context.Context, CreateOrderInput, string) (*domain.Order, error)
	ConfirmPayment(context.Context, string) error
	CancelOrder(context.Context, string, string) error
	GetOrder(context.Context, string) (*domain.Order, error)
}

type orderService struct {
	repo      repository.OrderRepository
	inventory client.Inventory
	catalog   client.Catalog
}

func NewOrderService(repo repository.OrderRepository, inv client.Inventory, cat client.Catalog) (Service, error) {
	if repo == nil || inv == nil || cat == nil {
		return nil, errors.New("order repository, inventory client and catalog client are required")
	}
	return &orderService{repo: repo, inventory: inv, catalog: cat}, nil
}

func (s *orderService) PlaceOrder(ctx context.Context, input CreateOrderInput, idemKey string) (*domain.Order, error) {
	customerID, err := uuid.Parse(input.CustomerID)
	if err != nil {
		return nil, badRequest("customer_id must be a UUID")
	}
	input.CustomerID = customerID.String()
	parsedKey, keyErr := uuid.Parse(idemKey)
	if keyErr != nil {
		return nil, badRequest("Idempotency-Key must be a UUID")
	}
	idemKey = parsedKey.String()
	if input.PickupAt.IsZero() || !input.PickupAt.After(time.Now().UTC()) {
		return nil, badRequest("pickup_at must be a future RFC 3339 timestamp")
	}
	items, err := normalizeItems(input.Items)
	if err != nil {
		return nil, err
	}

	requestHash, err := hashRequest(input.CustomerID, input.PickupAt.UTC(), items)
	if err != nil {
		return nil, err
	}
	if existing, getErr := s.repo.GetByIdempotencyKey(ctx, input.CustomerID, idemKey); getErr == nil {
		if existing.RequestHash != requestHash {
			return nil, conflict("IDEMPOTENCY_KEY_REUSED", "Idempotency-Key was already used with a different request")
		}
		return existing, nil
	} else if !errors.Is(getErr, repository.ErrNotFound) {
		return nil, dependencyError("ORDER_STORAGE_UNAVAILABLE", getErr)
	}

	flavorIDs := make([]string, 0, len(items))
	for id := range items {
		flavorIDs = append(flavorIDs, id)
	}
	sort.Strings(flavorIDs)
	flavors, err := s.catalog.BatchGetFlavors(ctx, flavorIDs)
	if err != nil {
		return nil, mapGRPCError("CATALOG", err)
	}
	byID := make(map[string]*catalogv1.Flavor, len(flavors))
	for _, flavor := range flavors {
		byID[flavor.GetId()] = flavor
	}

	orderID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(input.CustomerID+"/"+idemKey)).String()
	order := &domain.Order{
		ID: orderID, CustomerID: input.CustomerID, PickupAt: input.PickupAt.UTC(),
		Status: domain.StatusPendingPayment, IdempotencyKey: idemKey, RequestHash: requestHash,
		ConfirmIdempotencyKey: uuid.NewSHA1(uuid.NameSpaceURL, []byte(orderID+"/confirm")).String(),
		ReleaseIdempotencyKey: uuid.NewSHA1(uuid.NameSpaceURL, []byte(orderID+"/release")).String(),
		Currency:              "THB",
	}
	reservations := make([]*inventoryv1.PortionRequest, 0, len(items))
	for _, id := range flavorIDs {
		flavor := byID[id]
		if flavor == nil {
			return nil, dependencyError("CATALOG_INVALID_RESPONSE", errors.New("catalog omitted a requested flavor"))
		}
		if !flavor.GetActive() {
			return nil, conflict("FLAVOR_INACTIVE", "one or more selected flavors are not available for sale")
		}
		money := flavor.GetPrice()
		if money == nil || money.AmountMinor == nil || money.Currency == nil || money.GetCurrency() != order.Currency || money.GetAmountMinor() < 0 {
			return nil, dependencyError("CATALOG_INVALID_PRICE", errors.New("catalog returned an invalid price or currency"))
		}
		portions := items[id]
		if int64(portions) > 0 && money.GetAmountMinor() > math.MaxInt64/int64(portions) {
			return nil, badRequest("order amount exceeds supported range")
		}
		subtotal := money.GetAmountMinor() * int64(portions)
		if order.TotalAmountMinor > math.MaxInt64-subtotal {
			return nil, badRequest("order amount exceeds supported range")
		}
		order.TotalAmountMinor += subtotal
		order.Items = append(order.Items, domain.OrderItem{FlavorID: id, FlavorName: flavor.GetName(), Portions: portions, UnitPriceMinor: money.GetAmountMinor(), SubtotalMinor: subtotal})
		reservations = append(reservations, &inventoryv1.PortionRequest{FlavorId: id, Portions: portions})
	}

	reserveResp, err := s.inventory.ReservePortions(ctx, &inventoryv1.ReservePortionsRequest{OrderId: order.ID, IdempotencyKey: idemKey, Items: reservations})
	if err != nil {
		return nil, mapGRPCError("INVENTORY", err)
	}
	reservation := reserveResp.GetReservation()
	if reservation == nil || reservation.GetReservationId() == "" {
		return nil, dependencyError("INVENTORY_INVALID_RESPONSE", errors.New("inventory returned no reservation"))
	}
	if reservation.GetStatus() != inventoryv1.ReservationStatus_RESERVATION_STATUS_ACTIVE {
		return nil, conflict("RESERVATION_NOT_ACTIVE", "the reservation for this idempotent request is no longer active; retry with a new Idempotency-Key")
	}
	reservationID, err := uuid.Parse(reservation.GetReservationId())
	if err != nil {
		return nil, dependencyError("INVENTORY_INVALID_RESPONSE", errors.New("inventory returned an invalid reservation id"))
	}
	if reservation.GetExpiresAt() == nil || reservation.GetExpiresAt().CheckValid() != nil {
		_, _ = s.inventory.ReleaseReservation(ctx, &inventoryv1.ReleaseReservationRequest{ReservationId: reservationID.String(), OrderId: order.ID, IdempotencyKey: order.ReleaseIdempotencyKey, Reason: "ORDER_RESPONSE_INVALID"})
		return nil, dependencyError("INVENTORY_INVALID_RESPONSE", errors.New("inventory returned no valid reservation expiry"))
	}
	order.ReservationID = reservationID.String()
	order.ReservationExpiresAt = reservation.GetExpiresAt().AsTime().UTC()
	if !order.ReservationExpiresAt.After(time.Now().UTC()) {
		_, _ = s.inventory.ReleaseReservation(ctx, &inventoryv1.ReleaseReservationRequest{ReservationId: order.ReservationID, OrderId: order.ID, IdempotencyKey: order.ReleaseIdempotencyKey, Reason: "ORDER_RESERVATION_EXPIRED"})
		return nil, conflict("RESERVATION_EXPIRED", "the Inventory reservation expired before the order could be created")
	}

	if err := s.repo.Create(ctx, order); err != nil {
		// A concurrent retry or an ambiguous PostgreSQL result may have committed
		// the order. Reconcile before issuing a compensating release.
		if existing, lookupErr := s.repo.GetByIdempotencyKey(ctx, input.CustomerID, idemKey); lookupErr == nil {
			if existing.RequestHash != requestHash {
				return nil, conflict("IDEMPOTENCY_KEY_REUSED", "Idempotency-Key was already used with a different request")
			}
			return existing, nil
		} else if !errors.Is(lookupErr, repository.ErrNotFound) {
			return nil, dependencyError("ORDER_STORAGE_UNAVAILABLE", fmt.Errorf("save order: %v; reconcile idempotency key: %w", err, lookupErr))
		}
		// Inventory is independent of the Order database. Compensate with the same
		// stable key; its expiry worker remains the final safety net if this fails.
		_, releaseErr := s.inventory.ReleaseReservation(ctx, &inventoryv1.ReleaseReservationRequest{
			ReservationId: order.ReservationID, OrderId: order.ID, IdempotencyKey: order.ReleaseIdempotencyKey, Reason: "ORDER_PERSISTENCE_FAILED",
		})
		if releaseErr != nil {
			return nil, dependencyError("ORDER_SAVE_AND_RESERVATION_RELEASE_FAILED", fmt.Errorf("save order: %v; release reservation: %w", err, releaseErr))
		}
		return nil, dependencyError("ORDER_STORAGE_UNAVAILABLE", err)
	}
	return order, nil
}

func (s *orderService) ConfirmPayment(ctx context.Context, orderID string) error {
	order, err := s.repo.Get(ctx, orderID)
	if err != nil {
		return mapRepositoryError(err)
	}
	if order.Status == domain.StatusPaid {
		return nil
	}
	if order.Status != domain.StatusPendingPayment {
		return conflict("ORDER_STATE_CONFLICT", "order is not awaiting payment")
	}
	_, err = s.inventory.ConfirmReservation(ctx, &inventoryv1.ConfirmReservationRequest{
		ReservationId: order.ReservationID, OrderId: order.ID, IdempotencyKey: order.ConfirmIdempotencyKey,
	})
	if err != nil {
		return mapGRPCError("INVENTORY", err)
	}
	eventID := uuid.NewString()
	envelope := cloudEvent{SpecVersion: "1.0", ID: eventID, Source: "/gelatoflow/order-service", Type: "com.gelatoflow.order.placed.v1", Time: time.Now().UTC(), DataContentType: "application/json", Data: orderPlacedData{OrderID: order.ID, CustomerID: order.CustomerID, Status: string(domain.StatusPaid), PickupAt: order.PickupAt.UTC(), TotalAmountMinor: order.TotalAmountMinor, Currency: order.Currency, Items: order.Items}}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return dependencyError("ORDER_EVENT_ENCODING_FAILED", err)
	}
	if err := s.repo.MarkPaidWithOutbox(ctx, order.ID, &domain.OutboxEvent{ID: eventID, OrderID: order.ID, RoutingKey: "order.placed", Payload: payload}); err != nil {
		if errors.Is(err, repository.ErrInvalidTransition) {
			return conflict("ORDER_STATE_CONFLICT", "order changed state before payment confirmation")
		}
		return dependencyError("ORDER_STORAGE_UNAVAILABLE", err)
	}
	return nil
}

func (s *orderService) CancelOrder(ctx context.Context, orderID, reason string) error {
	if reason != "CUSTOMER_REQUEST" && reason != "PAYMENT_FAILED" {
		return badRequest("reason must be CUSTOMER_REQUEST or PAYMENT_FAILED")
	}
	order, err := s.repo.Get(ctx, orderID)
	if err != nil {
		return mapRepositoryError(err)
	}
	if order.Status == domain.StatusCancelled {
		return nil
	}
	if order.Status != domain.StatusPendingPayment {
		return conflict("ORDER_STATE_CONFLICT", "only pending-payment orders can be cancelled")
	}
	_, err = s.inventory.ReleaseReservation(ctx, &inventoryv1.ReleaseReservationRequest{ReservationId: order.ReservationID, OrderId: order.ID, IdempotencyKey: order.ReleaseIdempotencyKey, Reason: reason})
	if err != nil {
		return mapGRPCError("INVENTORY", err)
	}
	eventID := uuid.NewString()
	payload, err := json.Marshal(cloudEvent{SpecVersion: "1.0", ID: eventID, Source: "/gelatoflow/order-service", Type: "com.gelatoflow.order.cancelled.v1", Time: time.Now().UTC(), DataContentType: "application/json", Data: map[string]string{"order_id": order.ID, "reason": reason}})
	if err != nil {
		return dependencyError("ORDER_EVENT_ENCODING_FAILED", err)
	}
	if err := s.repo.MarkCancelled(ctx, order.ID, &domain.OutboxEvent{ID: eventID, OrderID: order.ID, RoutingKey: "order.cancelled", Payload: payload}); err != nil {
		if errors.Is(err, repository.ErrInvalidTransition) {
			return conflict("ORDER_STATE_CONFLICT", "order changed state before cancellation")
		}
		return dependencyError("ORDER_STORAGE_UNAVAILABLE", err)
	}
	return nil
}

func (s *orderService) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, badRequest("order_id must be a UUID")
	}
	order, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	return order, nil
}

func normalizeItems(items []OrderItemInput) (map[string]int32, error) {
	if len(items) == 0 || len(items) > 100 {
		return nil, badRequest("items must contain between 1 and 100 flavors")
	}
	result := make(map[string]int32, len(items))
	for _, item := range items {
		id, err := uuid.Parse(item.FlavorID)
		if err != nil {
			return nil, badRequest("flavor_id must be a UUID")
		}
		if item.Portions < 1 {
			return nil, badRequest("portions must be positive")
		}
		key := id.String()
		if _, exists := result[key]; exists {
			return nil, badRequest("each flavor_id may appear only once")
		}
		result[key] = item.Portions
	}
	return result, nil
}

func hashRequest(customerID string, pickupAt time.Time, items map[string]int32) (string, error) {
	type hashItem struct {
		FlavorID string `json:"flavor_id"`
		Portions int32  `json:"portions"`
	}
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	canonical := struct {
		CustomerID string     `json:"customer_id"`
		PickupAt   time.Time  `json:"pickup_at"`
		Items      []hashItem `json:"items"`
	}{CustomerID: customerID, PickupAt: pickupAt.UTC(), Items: make([]hashItem, 0, len(ids))}
	for _, id := range ids {
		canonical.Items = append(canonical.Items, hashItem{id, items[id]})
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func mapGRPCError(dependency string, err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return dependencyError(dependency+"_UNAVAILABLE", err)
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return badRequest(st.Message())
	case codes.Unauthenticated:
		return &AppError{Code: "DEPENDENCY_UNAUTHENTICATED", Message: st.Message(), HTTPStatus: 502}
	case codes.PermissionDenied:
		return &AppError{Code: "DEPENDENCY_FORBIDDEN", Message: st.Message(), HTTPStatus: 502}
	case codes.NotFound:
		return &AppError{Code: "RESOURCE_NOT_FOUND", Message: st.Message(), HTTPStatus: 404}
	case codes.FailedPrecondition, codes.AlreadyExists, codes.Aborted:
		return conflict("DEPENDENCY_CONFLICT", st.Message())
	case codes.DeadlineExceeded:
		return &AppError{Code: "DEPENDENCY_TIMEOUT", Message: "upstream service timed out", HTTPStatus: 504}
	case codes.Unavailable, codes.ResourceExhausted:
		return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "upstream service is unavailable", HTTPStatus: 503}
	default:
		return dependencyError(dependency+"_ERROR", err)
	}
}

func mapRepositoryError(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return &AppError{Code: "ORDER_NOT_FOUND", Message: "order not found", HTTPStatus: 404}
	}
	return dependencyError("ORDER_STORAGE_UNAVAILABLE", err)
}
func badRequest(message string) error {
	return &AppError{Code: "INVALID_ARGUMENT", Message: message, HTTPStatus: 400}
}
func conflict(code, message string) error {
	return &AppError{Code: code, Message: message, HTTPStatus: 409}
}
func dependencyError(code string, err error) error {
	return &AppError{Code: code, Message: "a required dependency could not complete the request", HTTPStatus: 503}
}

type cloudEvent struct {
	SpecVersion     string    `json:"specversion"`
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	Type            string    `json:"type"`
	Time            time.Time `json:"time"`
	DataContentType string    `json:"datacontenttype"`
	Data            any       `json:"data"`
}
type orderPlacedData struct {
	OrderID          string             `json:"order_id"`
	CustomerID       string             `json:"customer_id"`
	Status           string             `json:"status"`
	PickupAt         time.Time          `json:"pickup_at"`
	TotalAmountMinor int64              `json:"total_amount_minor"`
	Currency         string             `json:"currency"`
	Items            []domain.OrderItem `json:"items"`
}
