package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		return errors.New("connect to Order PostgreSQL: " + err.Error())
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if err = sqlDB.Ping(); err != nil {
		return errors.New("ping Order PostgreSQL: " + err.Error())
	}
	if err = db.AutoMigrate(&domain.Order{}, &domain.OrderItem{}, &domain.OutboxEvent{}); err != nil {
		return errors.New("migrate Order database: " + err.Error())
	}

	repo, err := repository.NewOrderRepository(db)
	if err != nil {
		return err
	}
	inv, err := client.NewInventoryClient(cfg.BatchInventoryGRPCURL, cfg.OrderServiceToken)
	if err != nil {
		return errors.New("connect Inventory gRPC: " + err.Error())
	}
	defer inv.Close()
	cat, err := client.NewCatalogClient(cfg.CatalogGRPCURL)
	if err != nil {
		return errors.New("connect Catalog gRPC: " + err.Error())
	}
	defer cat.Close()
	pub, err := publisher.NewRabbitMQPublisher(cfg.RabbitMQURL)
	if err != nil {
		return errors.New("connect RabbitMQ: " + err.Error())
	}
	defer pub.Close()

	svc, err := service.NewOrderService(repo, inv, cat)
	if err != nil {
		return err
	}
	h, err := handler.NewOrderHandler(svc, cfg.PaymentServiceToken)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go service.RunOutboxWorker(ctx, repo, pub)

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(logger.New(), recover.New())
	api := app.Group("/api/v1/orders")
	api.Post("/", h.CreateOrder)
	api.Get("/:id", h.GetOrder)
	api.Post("/:id/payment-succeeded", h.PaymentSucceeded)
	api.Post("/:id/payment-failed", h.PaymentFailed)
	api.Post("/:id/cancel", h.CancelOrder)
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })

	listenErr := make(chan error, 1)
	go func() { listenErr <- app.Listen(":" + cfg.Port) }()
	select {
	case err := <-listenErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			return err
		}
		if err := <-listenErr; err != nil {
			return err
		}
	}
	return nil
}
