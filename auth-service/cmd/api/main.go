package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"auth-service/config"
	"auth-service/internal/audit"
	"auth-service/internal/auth"
	"auth-service/internal/middleware"
	"auth-service/internal/repository"
	"auth-service/internal/router"
	"auth-service/internal/service"
	"auth-service/migrations"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()

	pool, err := pgxpool.New(startupCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("configure PostgreSQL failed: error_type=%T", err)
	}
	defer pool.Close()
	if err := pool.Ping(startupCtx); err != nil {
		log.Fatalf("connect to PostgreSQL failed: error_type=%T", err)
	}
	if err := migrations.Up(startupCtx, pool); err != nil {
		log.Fatalf("run migrations failed: error_type=%T", err)
	}

	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.JWTTTL)
	users := repository.NewPostgresUserRepository(pool)
	authService, err := service.NewAuthService(users, tokens, cfg.BcryptCost)
	if err != nil {
		log.Fatalf("configure credentials: %v", err)
	}

	app := fiber.New(fiber.Config{
		AppName:               "GelatoFlow Auth Service",
		BodyLimit:             middleware.CredentialBodyLimit,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          cfg.RequestTimeout + time.Second,
		IdleTimeout:           60 * time.Second,
		DisableStartupMessage: true,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			c.Set(fiber.HeaderCacheControl, "no-store")
			if fiberErr, ok := err.(*fiber.Error); ok && fiberErr.Code == fiber.StatusRequestEntityTooLarge {
				return c.Status(413).JSON(fiber.Map{"code": "REQUEST_TOO_LARGE", "message": "Request body exceeds 16 KiB"})
			}
			return fiber.DefaultErrorHandler(c, err)
		},
	})
	auditLogger := audit.NewPostgresAuditLogger(pool)
	rateLimiter := middleware.NewRateLimiter(5, 15*time.Minute)
	router.Setup(app, authService, tokens, router.Options{
		RequestTimeout: cfg.RequestTimeout,
		Pool:           pool,
		AuditLogger:    auditLogger,
		RateLimiter:    rateLimiter,
	})

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- app.Listen(":" + cfg.Port)
	}()

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil {
			log.Fatalf("serve HTTP failed: error_type=%T", err)
		}
	case <-shutdownSignal.Done():
		if err := app.Shutdown(); err != nil {
			log.Printf("graceful shutdown failed: error_type=%T", err)
		}
	}
}
