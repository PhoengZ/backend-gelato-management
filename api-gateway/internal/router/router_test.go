package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gelato/api-gateway/config"
	"github.com/gofiber/fiber/v2"
)

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
	SetupRoutes(app, config.Config{CatalogServiceURL: upstream.URL})

	tests := []struct {
		name       string
		method     string
		target     string
		wantTarget string
	}{
		{
			name:       "flavor collection with query",
			method:     http.MethodGet,
			target:     "/api/v1/flavors?active=true",
			wantTarget: "/api/v1/flavors?active=true",
		},
		{
			name:       "individual flavor",
			method:     http.MethodPatch,
			target:     "/api/v1/flavors/11111111-1111-4111-8111-111111111111",
			wantTarget: "/api/v1/flavors/11111111-1111-4111-8111-111111111111",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			resp, err := app.Test(req, -1)
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
			}

			got := <-received
			if got.method != tt.method {
				t.Errorf("upstream method = %q, want %q", got.method, tt.method)
			}
			if got.requestURI != tt.wantTarget {
				t.Errorf("upstream request URI = %q, want %q", got.requestURI, tt.wantTarget)
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
	SetupRoutes(app, config.Config{CatalogServiceURL: upstream.URL})

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
	SetupRoutes(app, config.Config{})

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
