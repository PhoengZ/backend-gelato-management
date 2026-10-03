package handler

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"

	"order-service/internal/client"
	"order-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type OrderHandler struct {
	svc          service.Service
	paymentToken string
}

func NewOrderHandler(svc service.Service, paymentToken string) (*OrderHandler, error) {
	if svc == nil || paymentToken == "" {
		return nil, errors.New("order service and payment service token are required")
	}
	return &OrderHandler{svc: svc, paymentToken: paymentToken}, nil
}

func (h *OrderHandler) CreateOrder(c *fiber.Ctx) error {
	var input service.CreateOrderInput
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return writeError(c, &service.AppError{Code: "INVALID_JSON", Message: "request body must be valid JSON", HTTPStatus: 400})
	}
	input.CustomerID, _ = authenticatedUserID(c)
	if input.CustomerID == "" {
		return writeError(c, &service.AppError{Code: "UNAUTHENTICATED", Message: "authenticated customer identity is required", HTTPStatus: 401})
	}
	ctx := c.UserContext()
	if bearer := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)); strings.HasPrefix(strings.ToLower(bearer), "bearer ") {
		ctx = client.WithUserAuthorization(ctx, bearer)
	}
	order, err := h.svc.PlaceOrder(ctx, input, c.Get("Idempotency-Key"))
	if err != nil {
		return writeError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(order)
}

func (h *OrderHandler) GetOrder(c *fiber.Ctx) error {
	userID, ok := authenticatedUserID(c)
	if !ok {
		return writeError(c, &service.AppError{Code: "UNAUTHENTICATED", Message: "authenticated customer identity is required", HTTPStatus: 401})
	}
	order, err := h.svc.GetOrder(c.UserContext(), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	if userID != order.CustomerID {
		return writeError(c, &service.AppError{Code: "FORBIDDEN", Message: "order does not belong to this customer", HTTPStatus: 403})
	}
	return c.JSON(order)
}

func (h *OrderHandler) PaymentSucceeded(c *fiber.Ctx) error {
	if err := h.authorizePaymentService(c); err != nil {
		return err
	}
	if err := h.svc.ConfirmPayment(c.UserContext(), c.Params("id")); err != nil {
		return writeError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *OrderHandler) PaymentFailed(c *fiber.Ctx) error {
	if err := h.authorizePaymentService(c); err != nil {
		return err
	}
	if err := h.svc.CancelOrder(c.UserContext(), c.Params("id"), "PAYMENT_FAILED"); err != nil {
		return writeError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *OrderHandler) authorizePaymentService(c *fiber.Ctx) error {
	provided := c.Get("X-Payment-Service-Token")
	if len(provided) != len(h.paymentToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.paymentToken)) != 1 {
		return writeError(c, &service.AppError{Code: "UNAUTHENTICATED", Message: "payment service authentication required", HTTPStatus: 401})
	}
	return nil
}

func (h *OrderHandler) CancelOrder(c *fiber.Ctx) error {
	var req struct {
		Reason string `json:"reason"`
	}
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return writeError(c, &service.AppError{Code: "INVALID_JSON", Message: "request body must be valid JSON", HTTPStatus: 400})
	}
	if req.Reason != "CUSTOMER_REQUEST" {
		return writeError(c, &service.AppError{Code: "INVALID_ARGUMENT", Message: "reason must be CUSTOMER_REQUEST", HTTPStatus: 400})
	}
	order, err := h.svc.GetOrder(c.UserContext(), c.Params("id"))
	if err != nil {
		return writeError(c, err)
	}
	userID, ok := authenticatedUserID(c)
	if !ok || userID != order.CustomerID {
		return writeError(c, &service.AppError{Code: "FORBIDDEN", Message: "order does not belong to this customer", HTTPStatus: 403})
	}
	if err := h.svc.CancelOrder(c.UserContext(), order.ID, req.Reason); err != nil {
		return writeError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func authenticatedUserID(c *fiber.Ctx) (string, bool) {
	id, err := uuid.Parse(c.Get("X-User-ID"))
	if err != nil {
		return "", false
	}
	return id.String(), true
}

func writeError(c *fiber.Ctx, err error) error {
	appErr, ok := err.(*service.AppError)
	if !ok {
		appErr = &service.AppError{Code: "INTERNAL_ERROR", Message: "an unexpected error occurred", HTTPStatus: 500}
	}
	return c.Status(appErr.HTTPStatus).JSON(fiber.Map{"code": appErr.Code, "message": appErr.Message})
}
