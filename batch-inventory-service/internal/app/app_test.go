package app

import (
	"context"
	"google.golang.org/grpc"
	"net"
	"net/http"
	"testing"
	"time"
)

func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func TestBindFailureAndImmediateCancellation(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	addr := freeAddress(t)
	if err = Serve(context.Background(), addr, occupied.Addr().String(), &http.Server{}, grpc.NewServer(), func(context.Context) {}); err == nil {
		t.Fatal("occupied gRPC bind succeeded")
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal("HTTP bind leaked", err)
	}
	l.Close()
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h, g := freeAddress(t), freeAddress(t)
		done := make(chan error, 1)
		go func() {
			done <- Serve(ctx, h, g, &http.Server{}, grpc.NewServer(), func(ctx context.Context) { <-ctx.Done() })
		}()
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("shutdown stalled")
		}
		for _, a := range []string{h, g} {
			l, err := net.Listen("tcp", a)
			if err != nil {
				t.Fatal("listener leaked", err)
			}
			l.Close()
		}
	}
}
