// Package server owns the lifetime of the two Catalog transports.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
)

// ListenAndServe binds both sockets before serving either transport. The caller
// closes storage only after this returns. Shutdown drains requests up to timeout.
func ListenAndServe(ctx context.Context, httpAddr, grpcAddr string, httpServer *fiber.App, grpcServer *grpc.Server, timeout time.Duration) error {
	httpListener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	defer httpListener.Close()
	grpcListener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC: %w", err)
	}
	defer grpcListener.Close()
	return Serve(ctx, httpListener, grpcListener, httpServer, grpcServer, timeout)
}

// Serve takes ownership of already-bound listeners, which also permits tests to
// allocate ephemeral ports without a close/rebind race.
func Serve(ctx context.Context, httpListener, grpcListener net.Listener, httpServer *fiber.App, grpcServer *grpc.Server, timeout time.Duration) error {
	defer httpListener.Close()
	defer grpcListener.Close()
	errorsCh := make(chan error, 2)
	httpReady := &readyListener{Listener: httpListener, ready: make(chan struct{})}
	grpcReady := &readyListener{Listener: grpcListener, ready: make(chan struct{})}
	go func() { defer httpReady.signal(); errorsCh <- httpServer.Listener(httpReady) }()
	go func() { defer grpcReady.signal(); errorsCh <- grpcServer.Serve(grpcReady) }()
	// Fiber shutdown before Serve registers its listener would miss the listener.
	// Wait for Accept (or an early Serve failure) before handling cancellation.
	<-httpReady.ready
	<-grpcReady.ready
	var serveErr error
	received := 0
	select {
	case <-ctx.Done():
	case serveErr = <-errorsCh:
		received++
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	httpStopped := make(chan error, 1)
	go func() { httpStopped <- httpServer.ShutdownWithContext(shutdownCtx) }()
	grpcStopped := make(chan struct{})
	go func() { grpcServer.GracefulStop(); close(grpcStopped) }()
	select {
	case <-grpcStopped:
	case <-shutdownCtx.Done():
		// GracefulStop can still wait for an application handler that ignores
		// cancellation. Never extend the process shutdown deadline waiting for it.
		go grpcServer.Stop()
	}
	httpErr := <-httpStopped
	for received < 2 {
		select {
		case err := <-errorsCh:
			if serveErr == nil {
				serveErr = err
			}
			received++
		case <-shutdownCtx.Done():
			return errors.Join(serveErr, httpErr)
		}
	}
	if errors.Is(serveErr, grpc.ErrServerStopped) || errors.Is(serveErr, net.ErrClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, httpErr)
}

type readyListener struct {
	net.Listener
	once  sync.Once
	ready chan struct{}
}

func (l *readyListener) signal() { l.once.Do(func() { close(l.ready) }) }
func (l *readyListener) Accept() (net.Conn, error) {
	l.signal()
	return l.Listener.Accept()
}
