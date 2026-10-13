package handler

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/auth"
	"notification-service/internal/hub"
	"notification-service/internal/stream"
)

func newServerWithBuffer(t *testing.T) (*httptest.Server, *hub.Hub, *stream.ReplayBuffer) {
	t.Helper()
	h := hub.New(8)
	buf := stream.NewDefaultReplayBuffer()
	h.OnPushToUser(buf.Add)

	srv := httptest.NewServer(New(auth.NewVerifier(secret, issuer, audience), h, 50*time.Millisecond, buf).Routes())
	t.Cleanup(srv.Close)
	return srv, h, buf
}

func TestStreamHeadersConfigured(t *testing.T) {
	srv, _, _ := newServerWithBuffer(t)
	user := uuid.New()
	resp := open(t, srv, "Bearer "+token(t, user, auth.RoleCustomer, nil))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if xab := resp.Header.Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", xab)
	}
	if conn := resp.Header.Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", conn)
	}
}

func TestStreamReplaysMissedEventsFromHeader(t *testing.T) {
	srv, h, buf := newServerWithBuffer(t)
	alice := uuid.New()

	evt1 := uuid.New()
	evt2 := uuid.New()
	evt3 := uuid.New()

	buf.Add(alice, hub.Notification{
		ID:         evt1,
		Type:       "order.confirmed",
		Message:    "Order confirmed",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 1},
	})
	buf.Add(alice, hub.Notification{
		ID:         evt2,
		Type:       "order.ready",
		Message:    "Order ready",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 2},
	})
	buf.Add(alice, hub.Notification{
		ID:         evt3,
		Type:       "order.ready",
		Message:    "Order ready update",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 3},
	})

	// Alice reconnects with Last-Event-ID = evt1. She should receive evt2 and evt3.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token(t, alice, auth.RoleCustomer, nil))
	req.Header.Set("Last-Event-ID", evt1.String())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)

	// First frame is : connected
	f0 := readFrame(t, reader)
	if f0["comment"] != ": connected" {
		t.Fatalf("first frame = %v, want : connected", f0)
	}

	// Next frame should be evt2
	f1 := readFrame(t, reader)
	if f1["id"] != evt2.String() || f1["event"] != "order.ready" {
		t.Fatalf("expected evt2, got %v", f1)
	}

	// Next frame should be evt3
	f2 := readFrame(t, reader)
	if f2["id"] != evt3.String() || f2["event"] != "order.ready" {
		t.Fatalf("expected evt3, got %v", f2)
	}

	// Now send a live event (evt4) and verify it arrives after replayed events
	evt4 := uuid.New()
	pushWhenReady(t, func() int {
		return h.PushToUser(alice, hub.Notification{
			ID:         evt4,
			Type:       "order.cancelled",
			Message:    "Order cancelled",
			OccurredAt: time.Now().UTC(),
			Data:       map[string]any{"num": 4},
		})
	})

	var f3 map[string]string
	for {
		f3 = readFrame(t, reader)
		if f3["comment"] == "" {
			break
		}
	}
	if f3["id"] != evt4.String() || f3["event"] != "order.cancelled" {
		t.Fatalf("expected live evt4, got %v", f3)
	}
}

func TestStreamReplaysMissedEventsFromQueryParam(t *testing.T) {
	srv, _, buf := newServerWithBuffer(t)
	bob := uuid.New()

	evt1 := uuid.New()
	evt2 := uuid.New()

	buf.Add(bob, hub.Notification{
		ID:         evt1,
		Type:       "order.confirmed",
		Message:    "Confirmed",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 1},
	})
	buf.Add(bob, hub.Notification{
		ID:         evt2,
		Type:       "order.ready",
		Message:    "Ready",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 2},
	})

	// Connect using query param ?last_event_id=
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications/stream?last_event_id="+evt1.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token(t, bob, auth.RoleCustomer, nil))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	readFrame(t, reader) // : connected

	f1 := readFrame(t, reader)
	if f1["id"] != evt2.String() {
		t.Fatalf("expected evt2 from query param replay, got %v", f1)
	}
}

func TestStreamDeduplicatesReplayedAndLiveEvents(t *testing.T) {
	srv, h, buf := newServerWithBuffer(t)
	user := uuid.New()

	evt1 := uuid.New()
	evt2 := uuid.New()

	buf.Add(user, hub.Notification{
		ID:         evt1,
		Type:       "order.confirmed",
		Message:    "Confirmed",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 1},
	})
	buf.Add(user, hub.Notification{
		ID:         evt2,
		Type:       "order.ready",
		Message:    "Ready",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 2},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications/stream?Last-Event-ID="+evt1.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token(t, user, auth.RoleCustomer, nil))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	readFrame(t, reader) // : connected

	// Replay frame for evt2
	f := readFrame(t, reader)
	if f["id"] != evt2.String() {
		t.Fatalf("expected evt2, got %v", f)
	}

	// Now attempt to push evt2 AGAIN live
	h.PushToUser(user, hub.Notification{
		ID:         evt2,
		Type:       "order.ready",
		Message:    "Duplicate Ready",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 2},
	})

	// And push evt3 live
	evt3 := uuid.New()
	h.PushToUser(user, hub.Notification{
		ID:         evt3,
		Type:       "order.cancelled",
		Message:    "Cancelled",
		OccurredAt: time.Now().UTC(),
		Data:       map[string]any{"num": 3},
	})

	// Next non-comment frame MUST be evt3 (evt2 should be skipped because it was already replayed)
	var nextFrame map[string]string
	for {
		nextFrame = readFrame(t, reader)
		if nextFrame["comment"] == "" {
			break
		}
	}

	if nextFrame["id"] != evt3.String() {
		t.Fatalf("expected evt3 after deduplication, got %v", nextFrame)
	}
}
