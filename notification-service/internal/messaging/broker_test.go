package messaging

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunRetriesUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var connected atomic.Bool
	done := make(chan struct{})

	go func() {
		// Nothing listens on port 1, so every dial fails and Run must keep backing off.
		Run(ctx, "amqp://guest:guest@127.0.0.1:1/", nil, &connected)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	if connected.Load() {
		t.Error("connected = true without a broker")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}
}
