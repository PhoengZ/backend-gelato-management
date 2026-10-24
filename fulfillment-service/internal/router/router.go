package router

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"fulfillment-service/internal/handler"
	authmw "fulfillment-service/internal/middleware"
)

// New builds the HTTP router. verifier is required: staff routes are never
// registered without authentication.
func New(h *handler.FulfillmentHandler, verifier *authmw.Verifier) *echo.Echo {
	if verifier == nil {
		panic("router: a JWT verifier is required")
	}
	authMid := authmw.NewAuthMiddleware(verifier)

	e := echo.New()

	// RequestID reuses an incoming X-Request-ID (e.g. from the Gateway) and the
	// default Logger format already prints it as "id".
	e.Use(middleware.RequestID())
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	e.GET("/health", h.Health)

	f := e.Group("/api/v1/fulfillments")

	// Static routes must be registered before parameterized routes in Echo
	f.GET("/slots/capacity", h.GetSlotCapacity) // GET /api/v1/fulfillments/slots/capacity?slot_time=...

	staff := authMid.RequireRoles(authmw.RoleStaff, authmw.RoleManager)
	f.GET("", h.List, staff)
	// Customers may read their own ticket; the handler enforces ownership.
	f.GET("/:order_id", h.Get, authMid.RequireRoles(authmw.RoleCustomer, authmw.RoleStaff, authmw.RoleManager))
	f.GET("/:order_id/history", h.History, authMid.RequireRoles(authmw.RoleManager))
	f.PATCH("/:order_id/status", h.UpdateStatus, staff)

	return e
}
