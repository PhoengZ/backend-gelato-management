package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestRateLimiterBlocksAfterMaxFailures(t *testing.T) {
	window := 10 * time.Minute
	limiter := NewRateLimiter(5, window)
	fakeTime := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return fakeTime }

	ip := "192.168.1.50"
	account := "victim@example.com"

	// 1 to 4 failed attempts should not be blocked
	for i := 1; i <= 4; i++ {
		limiter.RecordFailure(ip, account)
		blocked, _ := limiter.IsBlocked(ip, account)
		if blocked {
			t.Fatalf("attempt %d should not be blocked", i)
		}
	}

	// 5th failed attempt reaches the limit
	limiter.RecordFailure(ip, account)
	blocked, retry := limiter.IsBlocked(ip, account)
	if !blocked {
		t.Fatal("5th failure should trigger block")
	}
	if retry <= 0 || retry > int(window.Seconds())+1 {
		t.Fatalf("unexpected retry after: %d", retry)
	}

	// Another innocent IP attempting same account should also be blocked
	blockedAcc, _ := limiter.IsBlocked("10.0.0.1", account)
	if !blockedAcc {
		t.Fatal("account should be blocked regardless of IP")
	}

	// Same IP attempting another account should also be blocked
	blockedIP, _ := limiter.IsBlocked(ip, "other@example.com")
	if !blockedIP {
		t.Fatal("IP should be blocked regardless of account")
	}

	// Unrelated IP and account should not be blocked
	blockedUnrelated, _ := limiter.IsBlocked("10.0.0.2", "other@example.com")
	if blockedUnrelated {
		t.Fatal("unrelated IP and account should not be blocked")
	}
}

func TestRateLimiterResetClearsFailures(t *testing.T) {
	limiter := NewRateLimiter(5, 10*time.Minute)
	ip := "192.168.1.51"
	account := "user@example.com"

	for i := 0; i < 5; i++ {
		limiter.RecordFailure(ip, account)
	}
	blocked, _ := limiter.IsBlocked(ip, account)
	if !blocked {
		t.Fatal("expected to be blocked")
	}

	limiter.Reset(ip, account)
	blockedAfter, _ := limiter.IsBlocked(ip, account)
	if blockedAfter {
		t.Fatal("expected block to be cleared after reset")
	}
}

func TestRateLimiterExpiry(t *testing.T) {
	limiter := NewRateLimiter(5, 5*time.Minute)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	ip := "192.168.1.52"
	account := "expire@example.com"

	for i := 0; i < 5; i++ {
		limiter.RecordFailure(ip, account)
	}
	blocked, _ := limiter.IsBlocked(ip, account)
	if !blocked {
		t.Fatal("expected to be blocked initially")
	}

	// Advance time past the window
	now = now.Add(6 * time.Minute)
	blockedAfterExpiry, _ := limiter.IsBlocked(ip, account)
	if blockedAfterExpiry {
		t.Fatal("expected block to expire after window")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	limiter := NewRateLimiter(2, 5*time.Minute)
	app := fiber.New()
	app.Post("/login", limiter.Middleware(), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("POST", "/login", nil)
	resp, err := app.Test(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("first request should succeed, got status=%d err=%v", resp.StatusCode, err)
	}

	// Record failures for client IP (in app.Test, IP is 0.0.0.0 or 127.0.0.1 or "")
	limiter.RecordFailure("0.0.0.0", "test@example.com")
	limiter.RecordFailure("0.0.0.0", "test@example.com")
	limiter.RecordFailure("", "test@example.com")
	limiter.RecordFailure("", "test@example.com")

	req2 := httptest.NewRequest("POST", "/login", nil)
	respBlocked, err := app.Test(req2)
	if err != nil || respBlocked.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("blocked request should return 429, got %d", respBlocked.StatusCode)
	}
	if respBlocked.Header.Get(fiber.HeaderRetryAfter) == "" {
		t.Fatal("expected Retry-After header to be set")
	}
}
