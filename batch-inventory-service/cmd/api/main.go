package main

import (
	"batch-inventory-service/internal/app"
	"batch-inventory-service/internal/config"
	"batch-inventory-service/internal/logging"
	"batch-inventory-service/migrations"
	"context"
	"errors"
	"google.golang.org/grpc/grpclog"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetOutput(logging.NewProtocolWriter(os.Stderr))
	grpclog.SetLoggerV2(logging.NewGRPCLogger(os.Stderr))
	c, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = app.Run(ctx, c); err != nil {
		if errors.Is(err, migrations.ErrNotReady) {
			log.Fatal(migrations.ErrNotReady)
		}
		log.Fatalf("Inventory startup/serving failed: error_type=%T", err)
	}
}
