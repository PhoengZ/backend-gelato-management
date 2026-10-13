package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestSSEProxy_StreamFlushing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		_, _ = fmt.Fprint(w, "id: 1\nevent: order.ready\ndata: {\"order_id\":\"123\"}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	app := fiber.New()
	proxy := NewSSEProxy(upstream.URL)
	app.Get("/stream", proxy.Handler())

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(bodyBytes)

	if !strings.Contains(body, ": connected\n\n") {
		t.Errorf("expected body to contain ': connected\\n\\n', got: %q", body)
	}
	if !strings.Contains(body, "event: order.ready") {
		t.Errorf("expected body to contain 'event: order.ready', got: %q", body)
	}
	if !strings.Contains(body, "{\"order_id\":\"123\"}") {
		t.Errorf("expected body to contain order data, got: %q", body)
	}
}

func TestSSEProxy_HeaderForwarding(t *testing.T) {
	var capturedAuth, capturedCustom string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedCustom = r.Header.Get("X-Custom-Client")

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, ": ok\n\n")
	}))
	defer upstream.Close()

	app := fiber.New()
	proxy := NewSSEProxy(upstream.URL)
	app.Get("/stream", proxy.Handler())

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Authorization", "Bearer test-jwt-token")
	req.Header.Set("X-Custom-Client", "client-value")

	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if capturedAuth != "Bearer test-jwt-token" {
		t.Errorf("capturedAuth = %q, want %q", capturedAuth, "Bearer test-jwt-token")
	}
	if capturedCustom != "client-value" {
		t.Errorf("capturedCustom = %q, want %q", capturedCustom, "client-value")
	}
}

func TestSSEProxy_UpstreamErrors(t *testing.T) {
	t.Run("upstream 401 forwarded", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprint(w, `{"code":"UNAUTHORIZED","message":"token invalid"}`)
		}))
		defer upstream.Close()

		app := fiber.New()
		proxy := NewSSEProxy(upstream.URL)
		app.Get("/stream", proxy.Handler())

		req := httptest.NewRequest(http.MethodGet, "/stream", nil)
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("upstream unreachable returns 502 Bad Gateway", func(t *testing.T) {
		app := fiber.New()
		// Port that is guaranteed not running locally
		proxy := NewSSEProxy("http://127.0.0.1:59999")
		app.Get("/stream", proxy.Handler())

		req := httptest.NewRequest(http.MethodGet, "/stream", nil)
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
		}

		var body map[string]string
		dec := json.NewDecoder(resp.Body)
		if err := dec.Decode(&body); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if body["code"] != "BAD_GATEWAY" {
			t.Errorf("code = %q, want %q", body["code"], "BAD_GATEWAY")
		}
	})
}
