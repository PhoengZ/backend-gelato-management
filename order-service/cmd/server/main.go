package main

import (
	"log"

	"order-service/internal/client"
	"order-service/internal/config"
	"order-service/internal/domain"
	"order-service/internal/handler"
	"order-service/internal/publisher"
	"order-service/internal/repository"
	"order-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	// 1. Load configuration
	cfg := config.LoadConfig()

	// 2. Connect to Database (PostgreSQL)
	// We handle errors gracefully instead of fatal, so tests can run without DB if mocked
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		log.Printf("Warning: Failed to connect to database: %v", err)
	} else {
		log.Println("Connected to PostgreSQL database")
		// Auto Migrate models
		err = db.AutoMigrate(&domain.Order{}, &domain.OrderItem{})
		if err != nil {
			log.Fatalf("Failed to migrate database: %v", err)
		}
	}

	// 3. Connect to RabbitMQ
	pub, err := publisher.NewRabbitMQPublisher(cfg.RabbitMQURL)
	if err != nil {
		log.Printf("Warning: Failed to connect to RabbitMQ: %v", err)
	} else {
		defer pub.Close()
	}

	// 4. Connect to gRPC Batch Inventory Service
	invClient, err := client.NewInventoryClient(cfg.BatchInventoryGRPC_URL)
	if err != nil {
		log.Printf("Warning: Failed to connect to Batch Inventory Service: %v", err)
	} else {
		defer invClient.Close()
	}

	// 5. Initialize dependencies
	repo := repository.NewOrderRepository(db)
	svc := service.NewOrderService(repo, invClient, pub)
	orderHandler := handler.NewOrderHandler(svc)

	// 6. Setup Fiber App
	app := fiber.New()
	app.Use(logger.New())
	app.Use(recover.New())

	// API Routes
	api := app.Group("/api/v1")
	api.Post("/orders", orderHandler.CreateOrder)
	api.Patch("/orders/:id/status", orderHandler.UpdateOrderStatus)
	
	// Health check
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendString("Order Service is healthy")
	})

	// 7. Start Server
	log.Printf("Starting Order Service on port %s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
