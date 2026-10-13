package handler

import (
	"context"
	"reflect"
	"time"

	"github.com/gofiber/fiber/v2"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

type HealthHandler struct {
	db Pinger
}

func NewHealthHandler(db Pinger) *HealthHandler {
	if isNilPinger(db) {
		return &HealthHandler{db: nil}
	}
	return &HealthHandler{db: db}
}

// Health executes a deep health check verifying connectivity to the database.
// If the database ping fails, it returns 503 Service Unavailable.
func (h *HealthHandler) Health(c *fiber.Ctx) error {
	if !isNilPinger(h.db) {
		ctx, cancel := context.WithTimeout(c.UserContext(), 3*time.Second)
		defer cancel()

		if err := h.db.Ping(ctx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "unavailable",
				"error":  "database ping failed",
			})
		}
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"status": "ok",
	})
}

func isNilPinger(p Pinger) bool {
	if p == nil {
		return true
	}
	val := reflect.ValueOf(p)
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return true
	}
	return false
}
