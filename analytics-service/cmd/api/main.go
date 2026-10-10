package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"analytics-service/config"
	"analytics-service/internal/client"
	"analytics-service/internal/handler"
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
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}
	defer mongoClient.Disconnect(context.Background())

	db := mongoClient.Database("gelato_analytics")

	// Init layers
	repo := repository.NewAnalyticsRepository(db)
	eventRepo := repository.NewEventRepository(db)

	// Init Order Service gRPC client
	orderClient, err := client.NewOrderClient(cfg.OrderServiceGRPCAddr)
	if err != nil {
		log.Printf("Warning: failed to initialize order gRPC client: %v", err)
	} else {
		defer orderClient.Close()
	}

	svc := service.NewAnalyticsService(repo, eventRepo, orderClient)

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
	app := fiber.New(fiber.Config{
		ErrorHandler: handler.CustomErrorHandler,
	})
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
