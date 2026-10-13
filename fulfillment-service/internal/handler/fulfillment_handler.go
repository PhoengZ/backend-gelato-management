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
	List(ctx context.Context, statuses []models.Status) ([]models.Fulfillment, error)
	GetByOrderID(ctx context.Context, orderID uuid.UUID) (models.Fulfillment, error)
	UpdateStatus(ctx context.Context, orderID uuid.UUID, to models.Status) (models.Fulfillment, error)
	GetSlotCapacity(ctx context.Context, slotTime time.Time) (int, error)
}

type FulfillmentHandler struct {
	store Store
}

func NewFulfillmentHandler(store Store) *FulfillmentHandler {
	return &FulfillmentHandler{store: store}
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
	c.Logger().Errorf("internal error: %v", err)
	return fail(c, http.StatusInternalServerError, "INTERNAL_ERROR", "unexpected error", nil)
}

func parseOrderID(c echo.Context) (uuid.UUID, error) {
	return uuid.Parse(c.Param("order_id"))
}

// GET /api/v1/fulfillments?status=PREPARING,READY_FOR_PICKUP
func (h *FulfillmentHandler) List(c echo.Context) error {
	var statuses []models.Status
	if raw := c.QueryParam("status"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			s := models.Status(strings.TrimSpace(part))
			if !s.IsValid() {
				return fail(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid status filter",
					map[string]string{"status": "unknown status " + string(s)})
			}
			statuses = append(statuses, s)
		}
	}

	items, err := h.store.List(c.Request().Context(), statuses)
	if err != nil {
		return internalError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/fulfillments/:order_id
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
	return c.JSON(http.StatusOK, f)
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
