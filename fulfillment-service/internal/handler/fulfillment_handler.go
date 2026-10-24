package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"fulfillment-service/internal/middleware"
	"fulfillment-service/internal/models"
	"fulfillment-service/internal/repository"
)

// Store is the persistence the handler needs; *repository.FulfillmentRepository implements it.
type Store interface {
	List(ctx context.Context, filter models.ListFilter) ([]models.Fulfillment, error)
	GetByOrderID(ctx context.Context, orderID uuid.UUID) (models.Fulfillment, error)
	UpdateStatus(ctx context.Context, orderID uuid.UUID, to models.Status) (models.Fulfillment, error)
	GetSlotCapacity(ctx context.Context, slotTime time.Time) (int, error)
	GetHistory(ctx context.Context, fulfillmentID uuid.UUID) ([]models.FulfillmentStatusHistory, error)
}

// healthCheckTimeout bounds the database ping done by /health.
const healthCheckTimeout = 2 * time.Second

type FulfillmentHandler struct {
	store    Store
	brokerUp func() bool                     // nil when no broker is configured
	dbPing   func(ctx context.Context) error // nil skips the database check
}

func NewFulfillmentHandler(store Store) *FulfillmentHandler {
	return &FulfillmentHandler{store: store}
}

// WithBrokerStatus makes /health report 503 while connected() is false. Leave it
// unset when the service runs without a broker.
func (h *FulfillmentHandler) WithBrokerStatus(connected func() bool) *FulfillmentHandler {
	h.brokerUp = connected
	return h
}

// WithDBCheck makes /health report 503 while ping fails.
func (h *FulfillmentHandler) WithDBCheck(ping func(ctx context.Context) error) *FulfillmentHandler {
	h.dbPing = ping
	return h
}

// GET /health
func (h *FulfillmentHandler) Health(c echo.Context) error {
	if h.dbPing != nil {
		ctx, cancel := context.WithTimeout(c.Request().Context(), healthCheckTimeout)
		defer cancel()
		if err := h.dbPing(ctx); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "degraded", "database": "unreachable"})
		}
	}
	if h.brokerUp != nil && !h.brokerUp() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "degraded", "broker": "disconnected"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

type errorResponse struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

func fail(c echo.Context, status int, code, message string, details map[string]string) error {
	return c.JSON(status, errorResponse{Code: code, Message: message, Details: details})
}

func internalError(c echo.Context, err error) error {
	c.Logger().Errorf("internal error (request %s): %v", c.Response().Header().Get(echo.HeaderXRequestID), err)
	return fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "unexpected error", nil)
}

func parseOrderID(c echo.Context) (uuid.UUID, error) {
	return uuid.Parse(c.Param("order_id"))
}

// parseTimeParam reads an optional RFC 3339 query parameter.
func parseTimeParam(c echo.Context, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

// isStaff reports whether the caller may see every ticket.
func isStaff(c echo.Context) bool {
	role, ok := middleware.GetUserRole(c)
	return ok && (role == middleware.RoleStaff || role == middleware.RoleManager)
}

// GET /api/v1/fulfillments?status=PREPARING,READY_FOR_PICKUP&pickup_from=...&pickup_to=...
func (h *FulfillmentHandler) List(c echo.Context) error {
	var filter models.ListFilter
	if raw := c.QueryParam("status"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			s := models.Status(strings.TrimSpace(part))
			if !s.IsValid() {
				return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid status filter",
					map[string]string{"status": "unknown status " + string(s)})
			}
			filter.Statuses = append(filter.Statuses, s)
		}
	}

	var err error
	if filter.PickupFrom, err = parseTimeParam(c, "pickup_from"); err != nil {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid pickup time filter",
			map[string]string{"pickup_from": "must be RFC3339 format, e.g. 2026-10-13T00:00:00Z"})
	}
	if filter.PickupTo, err = parseTimeParam(c, "pickup_to"); err != nil {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid pickup time filter",
			map[string]string{"pickup_to": "must be RFC3339 format, e.g. 2026-10-13T00:00:00Z"})
	}
	if filter.PickupFrom != nil && filter.PickupTo != nil && !filter.PickupFrom.Before(*filter.PickupTo) {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid pickup time filter",
			map[string]string{"pickup_to": "must be after pickup_from"})
	}

	items, err := h.store.List(c.Request().Context(), filter)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/fulfillments/:order_id
// Staff and managers see every ticket. A customer sees only tickets for their
// own orders; anything else is reported as not found so order ids do not leak.
func (h *FulfillmentHandler) Get(c echo.Context) error {
	orderID, err := parseOrderID(c)
	if err != nil {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "order_id must be a valid UUID", nil)
	}

	f, err := h.store.GetByOrderID(c.Request().Context(), orderID)
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "NOT_FOUND", "fulfillment not found", nil)
	}
	if err != nil {
		return internalError(c, err)
	}
	if !isStaff(c) {
		userID, ok := middleware.GetUserID(c)
		if !ok || f.CustomerID == nil || *f.CustomerID != userID {
			return fail(c, http.StatusNotFound, "NOT_FOUND", "fulfillment not found", nil)
		}
	}
	return c.JSON(http.StatusOK, f)
}

// GET /api/v1/fulfillments/:order_id/history
func (h *FulfillmentHandler) History(c echo.Context) error {
	orderID, err := parseOrderID(c)
	if err != nil {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "order_id must be a valid UUID", nil)
	}

	ctx := c.Request().Context()
	f, err := h.store.GetByOrderID(ctx, orderID)
	if errors.Is(err, repository.ErrNotFound) {
		return fail(c, http.StatusNotFound, "NOT_FOUND", "fulfillment not found", nil)
	}
	if err != nil {
		return internalError(c, err)
	}

	items, err := h.store.GetHistory(ctx, f.ID)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// PATCH /api/v1/fulfillments/:order_id/status
func (h *FulfillmentHandler) UpdateStatus(c echo.Context) error {
	orderID, err := parseOrderID(c)
	if err != nil {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "order_id must be a valid UUID", nil)
	}

	var req models.UpdateStatusRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", nil)
	}
	if !req.Status.IsUpdatable() {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid status",
			map[string]string{"status": "must be READY_FOR_PICKUP or PICKED_UP"})
	}

	ctx := c.Request().Context()
	if actorID, ok := middleware.GetUserID(c); ok {
		ctx = repository.WithActorID(ctx, actorID)
	}

	f, err := h.store.UpdateStatus(ctx, orderID, req.Status)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return fail(c, http.StatusNotFound, "NOT_FOUND", "fulfillment not found", nil)
	case errors.Is(err, repository.ErrInvalidTransition):
		return fail(c, http.StatusConflict, "INVALID_TRANSITION", "status cannot move to the requested value", nil)
	case err != nil:
		return internalError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

// GET /api/v1/fulfillments/slots/capacity?slot_time=2026-10-13T14:00:00Z
func (h *FulfillmentHandler) GetSlotCapacity(c echo.Context) error {
	rawTime := c.QueryParam("slot_time")
	if strings.TrimSpace(rawTime) == "" {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "slot_time query parameter is required",
			map[string]string{"slot_time": "must not be empty"})
	}

	slotTime, err := time.Parse(time.RFC3339, rawTime)
	if err != nil {
		slotTime, err = time.Parse(time.RFC3339Nano, rawTime)
	}
	if err != nil {
		return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "slot_time must be a valid RFC3339 timestamp",
			map[string]string{"slot_time": "must be RFC3339 format, e.g. 2026-10-13T14:00:00Z"})
	}

	activeCount, err := h.store.GetSlotCapacity(c.Request().Context(), slotTime)
	if err != nil {
		return internalError(c, err)
	}

	start := slotTime.UTC().Truncate(15 * time.Minute)
	end := start.Add(15 * time.Minute)

	return c.JSON(http.StatusOK, models.SlotCapacityResponse{
		SlotTime:          slotTime.UTC(),
		SlotStart:         start,
		SlotEnd:           end,
		ActiveTicketCount: activeCount,
		ActiveCount:       activeCount,
	})
}
