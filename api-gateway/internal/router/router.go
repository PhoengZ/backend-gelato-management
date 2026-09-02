package router

import (
	"log"

	"github.com/gelato/api-gateway/config"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
)

func SetupRoutes(app *fiber.App, cfg config.Config) {
	api := app.Group("/api/v1")

	app.Use(func(c *fiber.Ctx) error {
		err := c.Next()
		if err != nil {
			log.Printf("Error processing route %s: %v", c.Path(), err)
		}
		return err
	})

	api.All("/auth/*", func(c *fiber.Ctx) error {
		url := cfg.AuthServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/catalog/*", func(c *fiber.Ctx) error {
		url := cfg.CatalogServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/orders/*", func(c *fiber.Ctx) error {
		url := cfg.OrderServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/inventory/*", func(c *fiber.Ctx) error {
		url := cfg.InventoryServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/fulfillments/*", func(c *fiber.Ctx) error {
		url := cfg.FulfillmentServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/analytics/*", func(c *fiber.Ctx) error {
		url := cfg.AnalyticsServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	api.All("/payments/*", func(c *fiber.Ctx) error {
		url := cfg.PaymentServiceURL + c.OriginalURL()
		return proxy.Do(c, url)
	})

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"status":  "ok",
			"message": "API Gateway is running",
		})
	})
}
