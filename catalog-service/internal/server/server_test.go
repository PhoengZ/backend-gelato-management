package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func listener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}
func app() *fiber.App {
	a := fiber.New(fiber.Config{DisableStartupMessage: true})
	a.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return a
}

func TestSecondBindFailureClosesFirstListener(t *testing.T) {
	httpPort := listener(t)
	address := httpPort.Addr().String()
	_ = httpPort.Close()
	occupied := listener(t)
	if err := ListenAndServe(context.Background(), address, occupied.Addr().String(), app(), grpc.NewServer(), time.Second); err == nil {
		t.Fatal("occupied gRPC port accepted")
	}
	again, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("HTTP listener leaked: %v", err)
	}
	_ = again.Close()
}

func TestImmediateCancellationClosesBothListeners(t *testing.T) {
	for range 10 {
		h, g := listener(t), listener(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := Serve(ctx, h, g, app(), grpc.NewServer(), time.Second); err != nil {
			t.Fatal(err)
		}
		for _, address := range []string{h.Addr().String(), g.Addr().String()} {
			l, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("listener leaked: %v", err)
			}
			_ = l.Close()
		}
	}
}

type blockingHealth struct {
	grpc_health_v1.UnimplementedHealthServer
	started  chan struct{}
	finished chan struct{}
	release  chan struct{}
}

func (h *blockingHealth) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	close(h.started)
	if h.release != nil {
		<-h.release
	} else {
		<-ctx.Done()
	}
	close(h.finished)
	return nil, ctx.Err()
}

func TestShutdownDoesNotWaitForUncooperativeHandler(t *testing.T) {
	h, g := listener(t), listener(t)
	s := grpc.NewServer()
	health := &blockingHealth{started: make(chan struct{}), finished: make(chan struct{}), release: make(chan struct{})}
	grpc_health_v1.RegisterHealthServer(s, health)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, h, g, app(), s, 100*time.Millisecond) }()
	conn, err := grpc.NewClient(g.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rpcCancel()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := grpc_health_v1.NewHealthClient(conn).Check(rpcCtx, &grpc_health_v1.HealthCheckRequest{})
		rpcDone <- err
	}()
	defer close(health.release)
	select {
	case <-health.started:
	case <-time.After(time.Second):
		t.Fatal("RPC not started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for uncooperative handler")
	}
	rpcCancel()
	select {
	case <-rpcDone:
	case <-time.After(time.Second):
		t.Fatal("client not cancelled")
	}
}

func TestShutdownForceStopsPendingRPCAndClosesHTTP(t *testing.T) {
	h, g := listener(t), listener(t)
	s := grpc.NewServer()
	health := &blockingHealth{started: make(chan struct{}), finished: make(chan struct{})}
	grpc_health_v1.RegisterHealthServer(s, health)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, h, g, app(), s, 150*time.Millisecond) }()
	conn, err := grpc.NewClient(g.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rpcCancel()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := grpc_health_v1.NewHealthClient(conn).Check(rpcCtx, &grpc_health_v1.HealthCheckRequest{})
		rpcDone <- err
	}()
	select {
	case <-health.started:
	case <-time.After(time.Second):
		t.Fatal("RPC not started")
	}
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + h.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown exceeded bound")
	}
	select {
	case <-health.finished:
	case <-time.After(time.Second):
		t.Fatal("handler not cancelled")
	}
	if err := <-rpcDone; err == nil {
		t.Fatal("pending RPC unexpectedly succeeded")
	}
	for _, address := range []string{h.Addr().String(), g.Addr().String()} {
		l, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("port not released: %v", err)
		}
		_ = l.Close()
	}
}
