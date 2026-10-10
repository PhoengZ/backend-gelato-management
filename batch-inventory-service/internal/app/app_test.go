package app

import (
	"context"
	"errors"
	"google.golang.org/grpc"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestBindFailureAndImmediateCancellation(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	var listeners []*net.TCPListener
	listen := func(network, addr string) (net.Listener, error) {
		l, err := net.Listen(network, addr)
		if err == nil {
			listeners = append(listeners, l.(*net.TCPListener))
		}
		return l, err
	}
	t.Cleanup(func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	})
	assertClosed := func(l *net.TCPListener) {
		t.Helper()
		// Inspect the original socket. Rebinding its old port could instead
		// observe an unrelated test/process that has already reused that port.
		_ = l.SetDeadline(time.Now())
		conn, err := l.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("listener %s not closed: %v", l.Addr(), err)
		}
	}
	if err = serve(context.Background(), "127.0.0.1:0", occupied.Addr().String(), &http.Server{}, grpc.NewServer(), func(context.Context) {}, listen); err == nil {
		t.Fatal("occupied gRPC bind succeeded")
	}
	if len(listeners) != 1 {
		t.Fatalf("expected HTTP listener before gRPC bind failure, got %d", len(listeners))
	}
	assertClosed(listeners[0])
	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		before := len(listeners)
		done := make(chan error, 1)
		go func() {
			done <- serve(ctx, "127.0.0.1:0", "127.0.0.1:0", &http.Server{}, grpc.NewServer(), func(ctx context.Context) { <-ctx.Done() }, listen)
		}()
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("shutdown stalled")
		}
		if len(listeners) != before+2 {
			t.Fatal("expected both HTTP and gRPC listeners")
		}
		for _, l := range listeners[before:] {
			assertClosed(l)
		}
	}
}
