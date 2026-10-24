package main

import (
	"context"
	"log"
	"os"
	"time"

	"auth-service/migrations"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("configure PostgreSQL failed: error_type=%T", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("connect to PostgreSQL failed: error_type=%T", err)
	}
	if err := migrations.Up(ctx, pool); err != nil {
		log.Fatalf("run migrations failed: error_type=%T", err)
	}
	log.Println("Auth Service migrations are up to date")
}
