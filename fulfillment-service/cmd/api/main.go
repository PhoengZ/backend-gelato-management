package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"kitchen-ticket-service/internal/config"
	"kitchen-ticket-service/internal/handler"
	"kitchen-ticket-service/internal/messaging"
	"kitchen-ticket-service/internal/repository"
	"kitchen-ticket-service/internal/router"
)

func main() {
	cfg := config.Load()

	ctx := context.Background()

	// --- PostgreSQL (via pgx) ---
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("unable to connect to postgres: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("postgres ping failed: %v", err)
	}
	log.Println("[db] connected to postgres")

	// --- RabbitMQ (via amqp091-go) — optional for this assignment ---
	publisher, err := messaging.NewPublisher(cfg.RabbitMQURL)
	if err != nil {
		log.Printf("[messaging] could not connect to RabbitMQ, continuing without it: %v", err)
		publisher = nil
	}
	defer publisher.Close()

	// --- wire layers ---
	repo := repository.NewTicketRepository(pool)
	h := handler.NewTicketHandler(repo, publisher)
	e := router.New(h)

	log.Printf("[http] kitchen-ticket-service listening on :%s", cfg.Port)
	if err := e.Start(":" + cfg.Port); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
