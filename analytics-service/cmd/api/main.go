package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"analytics-service/config"
	"analytics-service/internal/messaging"
	"analytics-service/internal/repository"
	"analytics-service/internal/router"
	"analytics-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	cfg := config.LoadConfig()

	// Connect to MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}
	defer client.Disconnect(context.Background())

	db := client.Database("gelato_analytics")

	// Init layers
	repo := repository.NewAnalyticsRepository(db)
	svc := service.NewAnalyticsService(repo)

	// Init RabbitMQ Consumer
	consumer, err := messaging.NewConsumer(cfg.RabbitMQURL, svc)
	if err != nil {
		log.Fatal("Failed to connect to RabbitMQ:", err)
	}
	defer consumer.Close()

	if err := consumer.Start(); err != nil {
		log.Fatal("Failed to start RabbitMQ consumer:", err)
	}

	// Setup Fiber App
	app := fiber.New()
	router.SetupRoutes(app, svc)

	// Start server gracefully
	go func() {
		if err := app.Listen(":" + cfg.Port); err != nil {
			log.Fatal("Fiber failed to start:", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down service...")
	_ = app.Shutdown()
}
