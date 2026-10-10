package middleware

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestLimiterBoundedStateAndExpiry(t *testing.T) {
	clock := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	l := newRateBudget(20, 2048)
	l.now = func() time.Time { return clock }
	l.maxKeys = 2
	for i := 0; i < 20; i++ {
		if ok, _ := l.allow("first"); !ok {
			t.Fatalf("request %d rejected", i+1)
		}
	}
	if ok, retry := l.allow("first"); ok || retry != 60 {
		t.Fatalf("limit not applied: allowed=%v retry=%d", ok, retry)
	}
	if ok, _ := l.allow("second"); !ok {
		t.Fatal("independent peer rejected")
	}
	if ok, _ := l.allow("third"); ok || len(l.windows) != 2 {
		t.Fatal("limiter key bound exceeded")
	}
	clock = clock.Add(time.Minute)
	if ok, _ := l.allow("third"); !ok || len(l.windows) != 1 {
		t.Fatal("expired peer records did not recover capacity")
	}
}

func TestLimiterConcurrentRequestsRespectBudget(t *testing.T) {
	l := newRateBudget(20, 2048)
	var wg sync.WaitGroup
	results := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ok, _ := l.allow("same-peer"); results <- ok }()
	}
	wg.Wait()
	close(results)
	allowed := 0
	for ok := range results {
		if ok {
			allowed++
		}
	}
	if allowed != 20 {
		t.Fatalf("concurrent requests allowed=%d, expected20", allowed)
	}
}

func TestRequestDeadlineCancelsServiceContext(t *testing.T) {
	app := fiber.New()
	var requestContext context.Context
	app.Use(RequestTimeout(time.Hour))
	app.Get("/", func(c *fiber.Ctx) error {
		requestContext = c.UserContext()
		if _, ok := requestContext.Deadline(); !ok {
			t.Error("request has no deadline")
		}
		return c.SendStatus(204)
	})
	response, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if requestContext.Err() != context.Canceled {
		t.Fatal("request context remained live after response")
	}
}

func TestAccountBudgetIsIndependentOfPeerBudget(t *testing.T) {
	l := NewCredentialLimiter()
	for i := 0; i < 20; i++ {
		if ok, _ := l.transport.allow(fmt.Sprintf("peer-%d", i)); !ok {
			t.Fatal("new transport peer blocked")
		}
		release, _ := l.Acquire("same@example.com")
		if release == nil {
			t.Fatalf("account attempt%d unexpectedly blocked", i+1)
		}
		release()
	}
	if ok, _ := l.transport.allow("yet-another-peer"); !ok {
		t.Fatal("transport peer blocked")
	}
	if release, retry := l.Acquire("same@example.com"); release != nil || retry < 1 || retry > 60 {
		t.Fatal("new peer bypassed account budget")
	}
	for i := 0; i < 40; i++ {
		if ok, _ := l.transport.allow("shared-bff-peer"); !ok {
			t.Fatal("shared transport peer blocked distinct accounts")
		}
		release, _ := l.Acquire(fmt.Sprintf("user%d@example.com", i))
		if release == nil {
			t.Fatal("distinct account inherited exhausted account budget")
		}
		release()
	}
}

func TestBusyCredentialAdmissionDoesNotSpendAccountBudget(t *testing.T) {
	l := NewCredentialLimiter()
	var held []func()
	for i := 0; i < 4; i++ {
		release, _ := l.Acquire(fmt.Sprintf("busy%d@example.com", i))
		if release == nil {
			t.Fatal("available work slot rejected")
		}
		held = append(held, release)
	}
	if release, retry := l.Acquire("waiting@example.com"); release != nil || retry != 1 {
		t.Fatal("overflowing credential work was queued or admitted")
	}
	if _, exists := l.accounts.windows["waiting@example.com"]; exists {
		t.Fatal("overload spent an account attempt")
	}
	held[0]()
	release, retry := l.Acquire("waiting@example.com")
	if release == nil || retry != 0 {
		t.Fatal("released work slot did not recover")
	}
	release()
	for _, release := range held[1:] {
		release()
	}
}
