package router

import (
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
		{http.MethodGet, "/api/v1/orders/123"},
		{http.MethodPost, "/api/v1/orders/create"},
		{http.MethodGet, "/api/v1/fulfillments/123"},
		{http.MethodGet, "/api/v1/analytics/sales"},
		{http.MethodPost, "/api/v1/payments/charge"},
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

	// Verify all spoofed identity headers are stripped
	for _, stripped := range []string{
		"X-User-Id", "X-User-Role", "X-User-Email",
		"X-Role", "X-Roles", "X-Authenticated-User-Id",
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
