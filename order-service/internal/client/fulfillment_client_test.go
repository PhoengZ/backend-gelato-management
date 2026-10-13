package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewFulfillmentClient(t *testing.T) {
	t.Run("empty URL returns error", func(t *testing.T) {
		_, err := NewFulfillmentClient("", 5*time.Second)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("valid URL succeeds with default timeout", func(t *testing.T) {
		cli, err := NewFulfillmentClient("http://localhost:8080/", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fc, ok := cli.(*fulfillmentClient)
		if !ok || fc.baseURL != "http://localhost:8080" || fc.timeout != 5*time.Second {
			t.Errorf("unexpected client configuration: %+v", fc)
		}
	})
}

func TestCheckSlotCapacity(t *testing.T) {
	slotStart := time.Date(2026, 10, 13, 14, 15, 0, 0, time.UTC)

	t.Run("200 OK with active_ticket_count", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("got method %s, want GET", r.Method)
			}
			if r.URL.Path != "/api/v1/fulfillments/slots/capacity" {
				t.Errorf("got path %s", r.URL.Path)
			}
			if r.URL.Query().Get("slot_start") == "" {
				t.Error("missing slot_start query param")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"active_ticket_count": 7}`))
		}))
		defer server.Close()

		cli, err := NewFulfillmentClient(server.URL, 2*time.Second)
		if err != nil {
			t.Fatalf("NewFulfillmentClient failed: %v", err)
		}

		count, err := cli.CheckSlotCapacity(context.Background(), slotStart)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 7 {
			t.Errorf("got count %d, want 7", count)
		}
	})

	t.Run("200 OK with active_count fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"active_count": 4}`))
		}))
		defer server.Close()

		cli, _ := NewFulfillmentClient(server.URL, 2*time.Second)
		count, err := cli.CheckSlotCapacity(context.Background(), slotStart)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 4 {
			t.Errorf("got count %d, want 4", count)
		}
	})

	t.Run("404 returns zero", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		cli, _ := NewFulfillmentClient(server.URL, 2*time.Second)
		count, err := cli.CheckSlotCapacity(context.Background(), slotStart)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 0 {
			t.Errorf("got count %d, want 0", count)
		}
	})

	t.Run("500 returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cli, _ := NewFulfillmentClient(server.URL, 2*time.Second)
		_, err := cli.CheckSlotCapacity(context.Background(), slotStart)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`invalid-json`))
		}))
		defer server.Close()

		cli, _ := NewFulfillmentClient(server.URL, 2*time.Second)
		_, err := cli.CheckSlotCapacity(context.Background(), slotStart)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("timeout returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cli, _ := NewFulfillmentClient(server.URL, 10*time.Millisecond)
		_, err := cli.CheckSlotCapacity(context.Background(), slotStart)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
	})
}
