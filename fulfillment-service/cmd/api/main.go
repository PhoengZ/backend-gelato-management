package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fulfillment-service/internal/config"
	"fulfillment-service/internal/handler"
	"fulfillment-service/internal/messaging"
	"fulfillment-service/internal/middleware"
	"fulfillment-service/internal/repository"
	"fulfillment-service/internal/router"
	"fulfillment-service/migrations"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("unable to connect to postgres: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("postgres ping failed: %v", err)
	}
	log.Println("[db] connected to postgres")

	if err := migrations.Apply(ctx, pool); err != nil {
		log.Fatalf("failed to apply migrations: %v", err)
	}
	log.Println("[db] migrations applied")

	repo := repository.NewFulfillmentRepository(pool)
	h := handler.NewFulfillmentHandler(repo).WithDBCheck(pool.Ping)

	// Drop published outbox rows, old dedupe records and tombstones past retention.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if n, err := repo.PurgeExpired(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
				log.Printf("[retention] purge failed: %v", err)
			} else if n > 0 {
				log.Printf("[retention] purged %d expired rows", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// RabbitMQ is optional: without it the REST API still works, events queue in the outbox.
	// When configured, the consumer and relay reconnect on their own and /health reports 503 while down.
	if cfg.RabbitMQURL == "" {
		log.Println("[messaging] RABBITMQ_URL not set, running without a message broker")
	} else {
		var brokerUp atomic.Bool
		h.WithBrokerStatus(brokerUp.Load)
		go messaging.Run(ctx, cfg.RabbitMQURL, repo, pool, &brokerUp)
	}

	verifier := middleware.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	e := router.New(h, verifier)
	e.Server.ReadHeaderTimeout = 5 * time.Second
	e.Server.ReadTimeout = 10 * time.Second
	e.Server.WriteTimeout = 15 * time.Second
	e.Server.IdleTimeout = 60 * time.Second

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.Shutdown(shutdownCtx)
	}()

	log.Printf("[http] fulfillment-service listening on :%s", cfg.Port)
	if err := e.Start(":" + cfg.Port); err != nil && ctx.Err() == nil {
		log.Fatalf("server stopped: %v", err)
	}
}
