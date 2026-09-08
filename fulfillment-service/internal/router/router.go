package router

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"kitchen-ticket-service/internal/handler"
)

func New(h *handler.TicketHandler) *echo.Echo {
	e := echo.New()

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	v1 := e.Group("/api/v1")

	tickets := v1.Group("/tickets")
	tickets.POST("", h.Create)     // POST   /api/v1/tickets
	tickets.GET("", h.GetAll)      // GET    /api/v1/tickets            (GET ALL)
	tickets.GET("/:id", h.GetOne)  // GET    /api/v1/tickets/:id        (GET ONE)
	tickets.PUT("/:id", h.Update)  // PUT    /api/v1/tickets/:id
	tickets.DELETE("/:id", h.Delete) // DELETE /api/v1/tickets/:id

	return e
}
