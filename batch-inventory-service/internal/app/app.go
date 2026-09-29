package app

import (
	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/catalog"
	"batch-inventory-service/internal/config"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/messaging"
	"batch-inventory-service/internal/transport"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

func Run(ctx context.Context, c config.Config) error {
	db, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return fmt.Errorf("invalid database configuration")
	}
	defer db.Close()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = db.Ping(startup); err != nil {
		return fmt.Errorf("database unavailable at startup")
	}
	var migrated bool
	if err = db.QueryRow(startup, "SELECT EXISTS(SELECT 1 FROM inventory_schema_migrations WHERE version='001_inventory.sql')").Scan(&migrated); err != nil || !migrated {
		return fmt.Errorf("run Inventory migrations before starting service")
	}
	cc, err := grpc.NewClient(c.CatalogAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer cc.Close()
	svc := inventory.New(db, catalog.New(cc, c.RequestTimeout), c.ReservationTTL)
	h := &http.Server{Handler: transport.NewHTTP(svc, auth.NewVerifier(c.JWTSecret, c.JWTIssuer, c.JWTAudience), c.RequestTimeout), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: c.RequestTimeout + time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	g := transport.NewGRPC(svc, c.OrderToken, c.RequestTimeout)
	return Serve(ctx, c.HTTPAddr, c.GRPCAddr, h, g, func(workerCtx context.Context) {
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			loop(workerCtx, c.SweepInterval, func(ctx context.Context) error { return svc.Sweep(ctx, 100) })
		}()
		if c.PublishEvents {
			workers.Add(1)
			go func() {
				defer workers.Done()
				loop(workerCtx, 200*time.Millisecond, func(ctx context.Context) error {
					_, err := messaging.DispatchOne(ctx, db, messaging.Rabbit{URL: c.RabbitURL})
					return err
				})
			}()
		}
		workers.Wait()
	})
}
func loop(ctx context.Context, interval time.Duration, work func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := work(run)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("inventory worker failed; durable work will retry")
			}
		}
	}
}
func Serve(ctx context.Context, httpAddr, grpcAddr string, h *http.Server, g *grpc.Server, workers func(context.Context)) error {
	hl, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("bind HTTP: %w", err)
	}
	defer hl.Close()
	gl, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("bind gRPC: %w", err)
	}
	defer gl.Close()
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); workers(workCtx) }()
	errs := make(chan error, 2)
	go func() { errs <- h.Serve(hl) }()
	go func() { errs <- g.Serve(gl) }()
	var result error
	select {
	case <-ctx.Done():
	case result = <-errs:
	}
	stop()
	drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpDone := make(chan struct{})
	go func() {
		defer close(httpDone)
		if h.Shutdown(drain) != nil {
			_ = h.Close()
		}
	}()
	grpcDone := make(chan struct{})
	go func() { g.GracefulStop(); close(grpcDone) }()
	select {
	case <-grpcDone:
	case <-drain.Done():
		go g.Stop()
	}
	select {
	case <-httpDone:
	case <-drain.Done():
		_ = h.Close()
	}
	select {
	case <-workerDone:
	case <-drain.Done():
	}
	if errors.Is(result, http.ErrServerClosed) || errors.Is(result, grpc.ErrServerStopped) || errors.Is(result, net.ErrClosed) {
		return nil
	}
	return result
}
