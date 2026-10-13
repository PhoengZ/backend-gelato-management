package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fulfillment-service/internal/config"
	"fulfillment-service/internal/handler"
	"fulfillment-service/internal/messaging"
	"fulfillment-service/internal/middleware"
	"fulfillment-service/internal/repository"
	"fulfillment-service/internal/router"
)

func main() {
	cfg := config.Load()

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

	repo := repository.NewFulfillmentRepository(pool)

	// RabbitMQ is optional: without it the REST API still works, events queue in the outbox.
	broker, err := messaging.Connect(cfg.RabbitMQURL)
	if err != nil {
		log.Printf("[messaging] could not connect to RabbitMQ, continuing without it: %v", err)
		broker = nil
	}
	if broker != nil {
		defer broker.Close()
		go messaging.NewRelay(pool, broker).Run(ctx)
		go func() {
			if err := broker.Consume(ctx, repo); err != nil && ctx.Err() == nil {
				log.Printf("[consumer] stopped: %v", err)
			}
		}()
	}

	verifier := middleware.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
	e := router.New(handler.NewFulfillmentHandler(repo), verifier)

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
