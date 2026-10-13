package router

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gelato/api-gateway/config"
	"github.com/gelato/api-gateway/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test_secret_that_is_longer_than_32_bytes"

func testToken(t *testing.T, role auth.Role) string {
	t.Helper()
	now := time.Now().UTC()
	claims := auth.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "11111111-2222-3333-4444-555555555555",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return raw
}

func testConfig(overrides ...func(*config.Config)) config.Config {
	cfg := config.Config{
		JWTSecret:   testSecret,
		JWTIssuer:   "gelatoflow-auth",
		JWTAudience: "gelatoflow-api",
	}
	for _, fn := range overrides {
		fn(&cfg)
	}
	return cfg
}

func TestCatalogFlavorRoutesProxyToCatalog(t *testing.T) {
	type receivedRequest struct {
		method     string
		requestURI string
	}

	received := make(chan receivedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- receivedRequest{method: r.Method, requestURI: r.RequestURI}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.CatalogServiceURL = upstream.URL
	}))

	validJWT := testToken(t, auth.RoleManager)

	tests := []struct {
		name       string
		method     string
		target     string
		token      string
		wantTarget string
		wantStatus int
	}{
		{
			name:       "flavor collection with query without token",
			method:     http.MethodGet,
			target:     "/api/v1/flavors?active=true",
			token:      "",
			wantTarget: "/api/v1/flavors?active=true",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "individual flavor with valid token",
			method:     http.MethodPatch,
			target:     "/api/v1/flavors/11111111-1111-4111-8111-111111111111",
			token:      validJWT,
			wantTarget: "/api/v1/flavors/11111111-1111-4111-8111-111111111111",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "individual flavor mutation without token fails",
			method:     http.MethodPatch,
			target:     "/api/v1/flavors/11111111-1111-4111-8111-111111111111",
			token:      "",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusNoContent {
				got := <-received
				if got.method != tt.method {
					t.Errorf("upstream method = %q, want %q", got.method, tt.method)
				}
				if got.requestURI != tt.wantTarget {
					t.Errorf("upstream request URI = %q, want %q", got.requestURI, tt.wantTarget)
				}
			}
		})
	}
}

func TestLegacyCatalogPrefixIsNotRegistered(t *testing.T) {
	upstreamCalls := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.CatalogServiceURL = upstream.URL
	}))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/catalog/flavors", nil), -1)
	if err != nil {
		t.Fatalf("request legacy route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	select {
	case <-upstreamCalls:
		t.Fatal("legacy Catalog route was unexpectedly proxied")
	default:
	}
}

func TestHealthRoute(t *testing.T) {
	app := fiber.New()
	SetupRoutes(app, testConfig())

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/health", nil), -1)
	if err != nil {
		t.Fatalf("request health route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Content-Type"); got != fiber.MIMEApplicationJSON {
		t.Fatalf("Content-Type = %q, want %q", got, fiber.MIMEApplicationJSON)
	}

	if got := resp.ContentLength; got <= 0 {
		t.Fatalf("response Content-Length = %d, want a JSON body", got)
	}
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	app := fiber.New()
	SetupRoutes(app, testConfig())

	protectedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPost, "/api/v1/flavors"},
		{http.MethodDelete, "/api/v1/flavors/123"},
		{http.MethodGet, "/api/v1/inventory/batches"},
		{http.MethodPost, "/api/v1/inventory/waste"},
		{http.MethodGet, "/api/v1/orders"},
		{http.MethodPost, "/api/v1/orders"},
		{http.MethodGet, "/api/v1/orders/123"},
		{http.MethodPost, "/api/v1/orders/create"},
		{http.MethodGet, "/api/v1/fulfillments/123"},
		{http.MethodGet, "/api/v1/analytics/sales"},
		{http.MethodGet, "/api/v1/payments"},
		{http.MethodPost, "/api/v1/payments/charge"},
		{http.MethodGet, "/api/v1/notifications/stream"},
	}

	for _, route := range protectedRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestPublicAndOptionalRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.AuthServiceURL = upstream.URL
		c.InventoryServiceURL = upstream.URL
	}))

	t.Run("public signup proxies without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup", nil)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("public login proxies without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("optional availability read proxies without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/availability", nil)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("optional availability read rejects invalid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/availability", nil)
		req.Header.Set("Authorization", "Bearer bad-token")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("optional availability read proxies with valid token", func(t *testing.T) {
		token := testToken(t, auth.RoleCustomer)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/availability", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})
}

func TestProxyPreservesBearerTokenAndStripsIdentityHeaders(t *testing.T) {
	var capturedHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.OrderServiceURL = upstream.URL
	}))

	token := testToken(t, auth.RoleManager)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/123", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-Id", "attacker-uuid")
	req.Header.Set("X-User-Role", "MANAGER")
	req.Header.Set("X-User-Email", "spoofed@evil.com")
	req.Header.Set("X-Role", "ADMIN")
	req.Header.Set("X-Roles", "SUPERUSER")
	req.Header.Set("X-Authenticated-User-Id", "attacker-uuid")
	req.Header.Set("X-Custom-Client", "legitimate-header")

	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Verify original Bearer token is strictly preserved
	if got := capturedHeaders.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization header modified or missing: %q", got)
	}

	// Verify verified claims headers are injected from JWT claims (not client's attacker-uuid)
	if got := capturedHeaders.Get("X-User-ID"); got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("X-User-ID = %q, want verified subject %q", got, "11111111-2222-3333-4444-555555555555")
	}
	if got := capturedHeaders.Get("X-User-Role"); got != "MANAGER" {
		t.Errorf("X-User-Role = %q, want %q", got, "MANAGER")
	}

	// Verify all other spoofed identity headers are stripped
	for _, stripped := range []string{
		"X-User-Email", "X-Role", "X-Roles", "X-Authenticated-User-Id",
	} {
		if got := capturedHeaders.Get(stripped); got != "" {
			t.Errorf("upstream unexpectedly received stripped header %s: %q", stripped, got)
		}
	}

	// Verify non-identity custom header remains
	if got := capturedHeaders.Get("X-Custom-Client"); got != "legitimate-header" {
		t.Errorf("non-identity header was unexpectedly removed: %q", got)
	}
}

func TestCORSAllowsIdempotencyHeaders(t *testing.T) {
	app := fiber.New()
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, Idempotency-Key, X-Idempotency-Key",
		AllowMethods: "GET, POST, PUT, DELETE, PATCH, OPTIONS",
	}))
	SetupRoutes(app, testConfig())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/orders/123", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Idempotency-Key, X-Idempotency-Key, Content-Type, Authorization")

	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("OPTIONS request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("CORS OPTIONS status = %d, want 204 or 200", resp.StatusCode)
	}

	allowedHeaders := resp.Header.Get("Access-Control-Allow-Headers")
	if allowedHeaders == "" {
		t.Fatal("missing Access-Control-Allow-Headers in CORS response")
	}
	for _, expected := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
		if !containsCaseInsensitive(allowedHeaders, expected) {
			t.Errorf("Access-Control-Allow-Headers %q missing %q", allowedHeaders, expected)
		}
	}
}

func containsCaseInsensitive(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func TestPaymentWebhookExemptFromAuth(t *testing.T) {
	var capturedURI, capturedMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.PaymentServiceURL = upstream.URL
	}))

	t.Run("POST /api/v1/payments/webhook proxies without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook", strings.NewReader(`{"id":"evt_123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Stripe-Signature", "t=123,v1=sig")

		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("webhook request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if capturedMethod != http.MethodPost {
			t.Errorf("method = %q, want %q", capturedMethod, http.MethodPost)
		}
		if capturedURI != "/api/v1/payments/webhook" {
			t.Errorf("URI = %q, want %q", capturedURI, "/api/v1/payments/webhook")
		}
	})

	t.Run("POST /api/v1/payments/charge requires auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/charge", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("charge request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})
}

func TestOrderRoutesMatchBothRootAndSubpath(t *testing.T) {
	type capturedReq struct {
		method string
		uri    string
	}
	received := make(chan capturedReq, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- capturedReq{method: r.Method, uri: r.RequestURI}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.OrderServiceURL = upstream.URL
	}))

	token := testToken(t, auth.RoleCustomer)

	t.Run("GET /api/v1/orders matches root orders route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		got := <-received
		if got.uri != "/api/v1/orders" {
			t.Errorf("upstream URI = %q, want %q", got.uri, "/api/v1/orders")
		}
	})

	t.Run("POST /api/v1/orders matches root orders route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		got := <-received
		if got.uri != "/api/v1/orders" {
			t.Errorf("upstream URI = %q, want %q", got.uri, "/api/v1/orders")
		}
	})

	t.Run("GET /api/v1/orders/abc matches subpath orders route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/abc", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		got := <-received
		if got.uri != "/api/v1/orders/abc" {
			t.Errorf("upstream URI = %q, want %q", got.uri, "/api/v1/orders/abc")
		}
	})
}

func TestNotificationStreamSSEProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flusher unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "id: 1\nevent: test\ndata: ok\n\n")
		flusher.Flush()
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.NotificationServiceURL = upstream.URL
	}))

	t.Run("unauthenticated request returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stream", nil)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("authenticated request streams unbuffered SSE", func(t *testing.T) {
		token := testToken(t, auth.RoleCustomer)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stream", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		body := string(bodyBytes)
		if !strings.Contains(body, ": connected\n\n") {
			t.Errorf("body missing ': connected', got %q", body)
		}
		if !strings.Contains(body, "event: test\ndata: ok\n\n") {
			t.Errorf("body missing event data, got %q", body)
		}
	})
}

func TestRouterRateLimiting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"abc"}`))
	}))
	t.Cleanup(upstream.Close)

	app := fiber.New()
	SetupRoutes(app, testConfig(func(c *config.Config) {
		c.AuthServiceURL = upstream.URL
	}))

	// Send 100 requests to /api/v1/auth/login (default limit is 100 req/min)
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d got status %d, want 200", i, resp.StatusCode)
		}
	}

	// 101st request should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("101st request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("101st request status = %d, want 429", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var errResp map[string]string
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("parse JSON error response: %v", err)
	}

	if errResp["code"] != "RATE_LIMITED" {
		t.Errorf("code = %q, want %q", errResp["code"], "RATE_LIMITED")
	}
}
