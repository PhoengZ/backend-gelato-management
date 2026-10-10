package router

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"fulfillment-service/internal/handler"
)

func New(h *handler.FulfillmentHandler) *echo.Echo {
	e := echo.New()

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	f := e.Group("/api/v1/fulfillments")
	f.GET("", h.List)                            // GET   /api/v1/fulfillments
	f.GET("/:order_id", h.Get)                   // GET   /api/v1/fulfillments/:order_id
	f.PATCH("/:order_id/status", h.UpdateStatus) // PATCH /api/v1/fulfillments/:order_id/status

	return e
}
