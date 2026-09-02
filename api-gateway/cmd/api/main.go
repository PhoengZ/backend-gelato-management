package main

import (
	"log"

	"github.com/gelato/api-gateway/config"
	"github.com/gelato/api-gateway/internal/router"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

func main() {
	// Load configuration
	cfg := config.LoadConfig()

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		AppName: "API Gateway",
	})

	// Setup middlewares
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*", // In production, restrict to frontend URL
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, DELETE, PATCH, OPTIONS",
	}))

	// Setup Routes
	router.SetupRoutes(app, cfg)

	// Start server
	log.Printf("Starting API Gateway on port %s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
