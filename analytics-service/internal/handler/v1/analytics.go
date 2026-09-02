package v1

import (
	"errors"
	"fmt"
	"log"

	"analytics-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

type AnalyticsHandler struct {
	service service.AnalyticsService
}

func NewAnalyticsHandler(service service.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{service: service}
}

func (h *AnalyticsHandler) GetAnalyticsSummary(c *fiber.Ctx) error {
	period := c.Query("period", "1w")

	summary, err := h.service.GetAnalyticsSummary(c.Context(), period)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPeriod) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("Unsupported period: %s. Valid values: 1d, 1w, 1m, 6m", period),
			})
		}
		log.Printf("error fetching analytics summary: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to fetch analytics",
		})
	}

	return c.JSON(summary)
}
