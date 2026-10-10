package main

import (
	"batch-inventory-service/internal/app"
	"batch-inventory-service/internal/config"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	c, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = app.Run(ctx, c); err != nil {
		log.Fatal(err)
	}
}
