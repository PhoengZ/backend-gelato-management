package router

import (
	v1 "analytics-service/internal/handler/v1"
	"analytics-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App, analyticsService service.AnalyticsService) {
	// Initialize handlers
	analyticsHandlerV1 := v1.NewAnalyticsHandler(analyticsService)

	// API Group
	api := app.Group("/api")

	// Version 1 routes
	apiV1 := api.Group("/v1")
	apiV1.Get("/analytics/summary", analyticsHandlerV1.GetAnalyticsSummary)
}
