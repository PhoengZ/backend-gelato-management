package handler

import (
	"analytics-service/internal/service"
	"github.com/gofiber/fiber/v2"
)

type AnalyticsHandler struct {
	service service.AnalyticsService
}

func NewAnalyticsHandler(service service.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{service: service}
}

func (h *AnalyticsHandler) GetAnalytics(c *fiber.Ctx) error {
	period := c.Query("period", "1w")

	analytics, err := h.service.GetAnalytics(c.Context(), period)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch analytics",
		})
	}

	return c.JSON(fiber.Map{
		"period": period,
		"data":   analytics,
	})
}

func (h *AnalyticsHandler) SetupRoutes(app *fiber.App) {
	api := app.Group("/api/v1")
	api.Get("/analytics", h.GetAnalytics)
}
