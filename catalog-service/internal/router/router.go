package router

import (
	"time"

	"catalog-service/internal/auth"
	v1 "catalog-service/internal/handler/v1"
	"catalog-service/internal/middleware"
	"catalog-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

func Setup(app *fiber.App, catalogService service.CatalogService, verifier *auth.Verifier, requestTimeout time.Duration) {
	handler := v1.NewCatalogHandler(catalogService)
	managerOnly := middleware.RequireManager(verifier)

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	flavors := app.Group("/api/v1/flavors", middleware.RequestTimeout(requestTimeout))
	flavors.Get("/", handler.List)
	flavors.Post("/", managerOnly, handler.Create)
	flavors.Get("/:flavor_id/recipe", managerOnly, handler.GetRecipe)
	flavors.Get("/:flavor_id", handler.Get)
	flavors.Patch("/:flavor_id", managerOnly, handler.Update)
	flavors.Delete("/:flavor_id", managerOnly, handler.Archive)
}
