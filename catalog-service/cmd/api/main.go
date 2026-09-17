package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"catalog-service/config"
	"catalog-service/internal/auth"
	"catalog-service/internal/repository"
	"catalog-service/internal/router"
	"catalog-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("configure Redis: %v", err)
	}
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()
	if err := redisClient.Ping(startupCtx).Err(); err != nil {
		log.Fatalf("connect to Redis: %v", err)
	}

	verifier := auth.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	flavors := repository.NewRedisFlavorRepository(redisClient, cfg.RedisKeyPrefix)
	catalogService := service.NewCatalogService(flavors)

	app := fiber.New(fiber.Config{
		AppName:               "GelatoFlow Catalog Service",
		BodyLimit:             1 * 1024 * 1024,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdleTimeout:           60 * time.Second,
		DisableStartupMessage: true,
	})
	router.Setup(app, catalogService, verifier, cfg.RequestTimeout)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- app.Listen(":" + cfg.Port)
	}()

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil {
			log.Fatalf("serve HTTP: %v", err)
		}
	case <-shutdownSignal.Done():
		if err := app.Shutdown(); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}
}
