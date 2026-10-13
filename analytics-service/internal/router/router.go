package router

import (
	v1 "analytics-service/internal/handler/v1"
	"analytics-service/internal/middleware"
	"analytics-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

func SetupRoutes(app *fiber.App, analyticsService service.AnalyticsService, verifiers ...*middleware.Verifier) {
	var verifier *middleware.Verifier
	if len(verifiers) > 0 && verifiers[0] != nil {
		verifier = verifiers[0]
	} else {
		verifier = middleware.NewVerifier("development_only_change_this_secret_32_bytes", "gelatoflow-auth", "gelatoflow-api")
	}

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Initialize handlers
	analyticsHandlerV1 := v1.NewAnalyticsHandler(analyticsService)

	// API Group
	api := app.Group("/api")

	// Version 1 routes
	apiV1 := api.Group("/v1")

	// Protected analytics routes requiring MANAGER role
	analytics := apiV1.Group("/analytics", middleware.RequireManager(verifier))
	analytics.Get("/summary", analyticsHandlerV1.GetAnalyticsSummary)
}
