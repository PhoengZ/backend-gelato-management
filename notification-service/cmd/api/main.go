package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"notification-service/internal/auth"
	"notification-service/internal/config"
	"notification-service/internal/handler"
	"notification-service/internal/hub"
	"notification-service/internal/messaging"
	"notification-service/internal/notify"
	"notification-service/internal/stream"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	replayBuffer := stream.NewReplayBuffer(500, 5*time.Minute)
	connections := hub.New(cfg.SubscriberBuffer)
	connections.OnPushToUser(replayBuffer.Add)
	connections.OnPushToRoles(replayBuffer.AddForRoles)

	// Drop per-user buffers once everything in them has expired.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				replayBuffer.CleanExpired()
			}
		}
	}()

	service := notify.New(connections, cfg.OrderOwnerTTL)

	h := handler.New(auth.NewVerifier(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience), connections, cfg.HeartbeatInterval, replayBuffer)

	// RabbitMQ is optional: without it the SSE endpoint still serves, but nothing is pushed.
	// When configured, the consumer reconnects on its own and /health reports 503 while it is down.
	if cfg.RabbitMQURL == "" {
		log.Println("[messaging] RABBITMQ_URL not set, running without a message broker")
	} else {
		var brokerUp atomic.Bool
		h.WithBrokerStatus(brokerUp.Load)
		go messaging.Run(ctx, cfg.RabbitMQURL, service, &brokerUp)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: h.Routes(),
		// No WriteTimeout: SSE responses stay open indefinitely.
		ReadHeaderTimeout: 10 * time.Second,
		// Request contexts derive from ctx, so open streams end when shutdown starts.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("[http] notification-service listening on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server stopped: %v", err)
	}
}
