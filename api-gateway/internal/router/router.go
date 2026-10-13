package router

import (
	"log"

	"github.com/gelato/api-gateway/config"
	"github.com/gelato/api-gateway/internal/auth"
	"github.com/gelato/api-gateway/internal/middleware"
	"github.com/gelato/api-gateway/internal/proxy"
	"github.com/gofiber/fiber/v2"
	fiberproxy "github.com/gofiber/fiber/v2/middleware/proxy"
)

func SetupRoutes(app *fiber.App, cfg config.Config) {
	app.Use(func(c *fiber.Ctx) error {
		err := c.Next()
		if err != nil {
			log.Printf("Error processing route %s: %v", c.Path(), err)
		}
		return err
	})

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status":  "ok",
			"message": "API Gateway is running",
		})
	})

	api := app.Group("/api/v1")

	// Strip spoofed client identity headers globally for all API routes before forwarding.
	api.Use(middleware.StripIdentityHeaders())

	verifier := auth.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	requireAuth := middleware.RequireAuth(verifier)
	optAuth := middleware.AuthenticateIfPresent(verifier)

	publicLimiter := middleware.PublicRateLimiter()
	authLimiter := middleware.AuthenticatedRateLimiter()

	// Auth routes: signup & login are public; /auth/me and others require authentication
	authProxy := func(c *fiber.Ctx) error {
		url := cfg.AuthServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.Post("/auth/signup", publicLimiter, authProxy)
	api.Post("/auth/login", publicLimiter, authProxy)
	api.All("/auth/*", requireAuth, authLimiter, authProxy)

	// Catalog routes: reads permit optional auth; mutations require authentication
	catalogProxy := func(c *fiber.Ctx) error {
		url := cfg.CatalogServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.Get("/flavors", optAuth, catalogProxy)
	api.Get("/flavors/*", optAuth, catalogProxy)
	api.All("/flavors", requireAuth, authLimiter, catalogProxy)
	api.All("/flavors/*", requireAuth, authLimiter, catalogProxy)

	// Inventory routes: availability read permits optional auth; batch mutations/waste require authentication
	inventoryProxy := func(c *fiber.Ctx) error {
		url := cfg.InventoryServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.Get("/inventory/availability", optAuth, inventoryProxy)
	api.All("/inventory/*", requireAuth, authLimiter, inventoryProxy)

	// Protected downstream routes: match both /orders and /orders/*
	orderProxy := func(c *fiber.Ctx) error {
		url := cfg.OrderServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.All("/orders", requireAuth, authLimiter, orderProxy)
	api.All("/orders/*", requireAuth, authLimiter, orderProxy)

	fulfillmentProxy := func(c *fiber.Ctx) error {
		url := cfg.FulfillmentServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.All("/fulfillments/*", requireAuth, authLimiter, fulfillmentProxy)

	analyticsProxy := func(c *fiber.Ctx) error {
		url := cfg.AnalyticsServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.All("/analytics/*", requireAuth, authLimiter, analyticsProxy)

	// Payment routes: webhook is exempt from requireAuth (verified via Stripe signature downstream).
	// Other /payments and /payments/* routes require authentication.
	paymentProxy := func(c *fiber.Ctx) error {
		url := cfg.PaymentServiceURL + c.OriginalURL()
		return fiberproxy.Do(c, url)
	}
	api.Post("/payments/webhook", publicLimiter, paymentProxy)
	api.All("/payments", requireAuth, authLimiter, paymentProxy)
	api.All("/payments/*", requireAuth, authLimiter, paymentProxy)

	// Notification SSE stream: routed to unbuffered streaming proxy to eliminate buffering
	sseProxy := proxy.NewSSEProxy(cfg.NotificationServiceURL)
	api.Get("/notifications/stream", requireAuth, authLimiter, sseProxy.Handler())
}
