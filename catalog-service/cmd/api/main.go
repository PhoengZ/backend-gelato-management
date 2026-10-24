package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"catalog-service/config"
	"catalog-service/internal/auth"
	"catalog-service/internal/logging"
	"catalog-service/internal/repository"
	"catalog-service/internal/router"
	"catalog-service/internal/server"
	"catalog-service/internal/service"
	cataloggrpc "catalog-service/internal/transport/grpc"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/grpclog"
)

func main() {
	log.SetOutput(logging.NewRedactorWriter(os.Stderr))
	grpclog.SetLoggerV2(logging.NewGRPCLogger(os.Stderr))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatalf("Catalog startup/serving failed: error_type=%T", err)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return errors.New("configure Redis failed; check Redis URL configuration")
	}
	redisOptions.ContextTimeoutEnabled = true
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()

	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()
	if err := redisClient.Ping(startupCtx).Err(); err != nil {
		return errors.New("connect to Redis failed; check storage connectivity")
	}

	verifier := auth.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	flavors := repository.NewRedisFlavorRepository(redisClient, cfg.RedisKeyPrefix)
	if err := flavors.PrepareIdempotency(startupCtx); err != nil {
		return errors.New("prepare Catalog idempotency storage failed; check storage access")
	}
	catalogService := service.NewCatalogService(flavors)

	app := fiber.New(fiber.Config{
		AppName:               "GelatoFlow Catalog Service",
		BodyLimit:             1 * 1024 * 1024,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		IdleTimeout:           60 * time.Second,
		DisableStartupMessage: true,
	})

	// Mount centralized redactor logger middleware
	app.Use(logging.RedactorLoggerMiddleware())

	router.Setup(app, catalogService, verifier, cfg.RequestTimeout, redisClient)

	grpcServer := cataloggrpc.NewServer(catalogService, verifier, cfg.RequestTimeout)
	return server.ListenAndServe(ctx, ":"+cfg.Port, cfg.GRPCAddr, app, grpcServer, 5*time.Second)
}
