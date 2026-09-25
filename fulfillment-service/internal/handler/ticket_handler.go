package handler

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"kitchen-ticket-service/internal/messaging"
	"kitchen-ticket-service/internal/models"
	"kitchen-ticket-service/internal/repository"
)

type TicketHandler struct {
	repo      *repository.TicketRepository
	publisher *messaging.Publisher
}

func NewTicketHandler(repo *repository.TicketRepository, publisher *messaging.Publisher) *TicketHandler {
	return &TicketHandler{repo: repo, publisher: publisher}
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// POST /tickets
func (h *TicketHandler) Create(c echo.Context) error {
	var req models.CreateTicketRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errorResponse{"BAD_REQUEST", "invalid request body"})
	}
	if req.OrderID == "" || req.PickupSlot == "" || req.QueueNumber <= 0 {
		return c.JSON(http.StatusBadRequest, errorResponse{"VALIDATION_ERROR", "order_id, pickup_slot and a positive queue_number are required"})
	}

	ticket, err := h.repo.Create(c.Request().Context(), req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errorResponse{"INTERNAL_ERROR", err.Error()})
	}

	h.publisher.Publish(c.Request().Context(), "fulfillment.ticket_created", ticket)
	return c.JSON(http.StatusCreated, ticket)
}

// GET /tickets/:id
func (h *TicketHandler) GetOne(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errorResponse{"BAD_REQUEST", "id must be a valid UUID"})
	}

	ticket, err := h.repo.GetByID(c.Request().Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errorResponse{"NOT_FOUND", "kitchen ticket not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errorResponse{"INTERNAL_ERROR", err.Error()})
	}
	return c.JSON(http.StatusOK, ticket)
}

// GET /tickets?status=PREPARING
func (h *TicketHandler) GetAll(c echo.Context) error {
	status := c.QueryParam("status")

	tickets, err := h.repo.GetAll(c.Request().Context(), status)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errorResponse{"INTERNAL_ERROR", err.Error()})
	}
	return c.JSON(http.StatusOK, tickets)
}

// PUT /tickets/:id
// Body: {"status": "READY_FOR_PICKUP"} — mirrors markOrderReady / markOrderPickedUp.
func (h *TicketHandler) Update(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errorResponse{"BAD_REQUEST", "id must be a valid UUID"})
	}

	var req models.UpdateTicketRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errorResponse{"BAD_REQUEST", "invalid request body"})
	}
	if !req.Status.IsValid() {
		return c.JSON(http.StatusBadRequest, errorResponse{"VALIDATION_ERROR", "status must be one of PREPARING, READY_FOR_PICKUP, PICKED_UP"})
	}

	ticket, err := h.repo.UpdateStatus(c.Request().Context(), id, req.Status)
	if errors.Is(err, repository.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errorResponse{"NOT_FOUND", "kitchen ticket not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errorResponse{"INTERNAL_ERROR", err.Error()})
	}

	if ticket.Status == models.StatusReady {
		h.publisher.Publish(c.Request().Context(), "fulfillment.ticket_ready", ticket)
	}
	return c.JSON(http.StatusOK, ticket)
}

// DELETE /tickets/:id
func (h *TicketHandler) Delete(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errorResponse{"BAD_REQUEST", "id must be a valid UUID"})
	}

	err = h.repo.Delete(c.Request().Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errorResponse{"NOT_FOUND", "kitchen ticket not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errorResponse{"INTERNAL_ERROR", err.Error()})
	}
	return c.NoContent(http.StatusNoContent)
}
