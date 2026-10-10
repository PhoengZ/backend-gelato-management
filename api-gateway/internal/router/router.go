package router

import (
	"log"

	"github.com/gelato/api-gateway/config"
	"github.com/gelato/api-gateway/internal/auth"
	"github.com/gelato/api-gateway/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
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

	// Auth routes: signup & login are public; /auth/me and others require authentication
	authProxy := func(c *fiber.Ctx) error {
		url := cfg.AuthServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	}
	api.Post("/auth/signup", authProxy)
	api.Post("/auth/login", authProxy)
	api.All("/auth/*", requireAuth, authProxy)

	// Catalog routes: reads permit optional auth; mutations require authentication
	catalogProxy := func(c *fiber.Ctx) error {
		url := cfg.CatalogServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	}
	api.Get("/flavors", optAuth, catalogProxy)
	api.Get("/flavors/*", optAuth, catalogProxy)
	api.All("/flavors", requireAuth, catalogProxy)
	api.All("/flavors/*", requireAuth, catalogProxy)

	// Inventory routes: availability read permits optional auth; batch mutations/waste require authentication
	inventoryProxy := func(c *fiber.Ctx) error {
		url := cfg.InventoryServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	}
	api.Get("/inventory/availability", optAuth, inventoryProxy)
	api.All("/inventory/*", requireAuth, inventoryProxy)

	// Protected downstream routes
	api.All("/orders/*", requireAuth, func(c *fiber.Ctx) error {
		url := cfg.OrderServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/fulfillments/*", requireAuth, func(c *fiber.Ctx) error {
		url := cfg.FulfillmentServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/analytics/*", requireAuth, func(c *fiber.Ctx) error {
		url := cfg.AnalyticsServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/payments/*", requireAuth, func(c *fiber.Ctx) error {
		url := cfg.PaymentServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})
}
