package router

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"fulfillment-service/internal/handler"
	authmw "fulfillment-service/internal/middleware"
)

func New(h *handler.FulfillmentHandler, verifier ...*authmw.Verifier) *echo.Echo {
	e := echo.New()

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	var authMid *authmw.AuthMiddleware
	if len(verifier) > 0 && verifier[0] != nil {
		authMid = authmw.NewAuthMiddleware(verifier[0])
	}

	f := e.Group("/api/v1/fulfillments")

	// Static routes must be registered before parameterized routes in Echo
	f.GET("/slots/capacity", h.GetSlotCapacity) // GET /api/v1/fulfillments/slots/capacity?slot_time=...

	if authMid != nil {
		f.GET("", h.List, authMid.RequireRoles(authmw.RoleStaff, authmw.RoleManager))
		f.GET("/:order_id", h.Get, authMid.RequireRoles(authmw.RoleStaff, authmw.RoleManager))
		f.PATCH("/:order_id/status", h.UpdateStatus, authMid.RequireRoles(authmw.RoleStaff, authmw.RoleManager))
	} else {
		f.GET("", h.List)
		f.GET("/:order_id", h.Get)
		f.PATCH("/:order_id/status", h.UpdateStatus)
	}

	return e
}
