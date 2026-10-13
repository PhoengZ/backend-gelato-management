package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestPublicRateLimiter(t *testing.T) {
	app := fiber.New()
	// Use small limit for fast test
	app.Get("/public", PublicRateLimiter(RateLimiterConfig{
		Max:        3,
		Expiration: 1 * time.Minute,
	}), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/public", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d", i, resp.StatusCode, http.StatusOK)
		}
	}

	// 4th request should hit rate limit
	req := httptest.NewRequest(http.MethodGet, "/public", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("4th request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
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
	if errResp["message"] != "Too many requests" {
		t.Errorf("message = %q, want %q", errResp["message"], "Too many requests")
	}
}

func TestAuthenticatedRateLimiter(t *testing.T) {
	app := fiber.New()
	// Simulate auth setting LocalUserID
	app.Use(func(c *fiber.Ctx) error {
		userID := c.Get("X-Test-User-ID")
		if userID != "" {
			c.Locals(LocalUserID, userID)
		}
		return c.Next()
	})

	app.Get("/authenticated", AuthenticatedRateLimiter(RateLimiterConfig{
		Max:        2,
		Expiration: 1 * time.Minute,
	}), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	// User 1 makes 2 requests (reaches limit)
	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/authenticated", nil)
		req.Header.Set("X-Test-User-ID", "user-111")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("user-1 request %d failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("user-1 request %d status = %d, want %d", i, resp.StatusCode, http.StatusOK)
		}
	}

	// User 1 3rd request should fail with 429
	req := httptest.NewRequest(http.MethodGet, "/authenticated", nil)
	req.Header.Set("X-Test-User-ID", "user-111")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("user-1 3rd request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}

	// User 2 should NOT be blocked by user 1's limit
	req2 := httptest.NewRequest(http.MethodGet, "/authenticated", nil)
	req2.Header.Set("X-Test-User-ID", "user-222")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("user-2 request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("user-2 status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestRateLimiterDefaults(t *testing.T) {
	pub := PublicRateLimiter()
	if pub == nil {
		t.Fatal("PublicRateLimiter returned nil handler")
	}

	auth := AuthenticatedRateLimiter()
	if auth == nil {
		t.Fatal("AuthenticatedRateLimiter returned nil handler")
	}
}
