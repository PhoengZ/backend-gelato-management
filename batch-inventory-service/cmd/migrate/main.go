package main

import (
	"batch-inventory-service/migrations"
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"os"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := pgxpool.New(ctx, raw)
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer db.Close()
	if err = migrations.Apply(ctx, db); err != nil {
		log.Fatal("Inventory migration failed; check database connectivity and schema")
	}
	log.Print("Inventory migrations applied")
}
