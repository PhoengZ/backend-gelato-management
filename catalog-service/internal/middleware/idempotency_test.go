package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestIdempotencyPassThroughWhenNoKey(t *testing.T) {
	app := fiber.New()
	app.Post("/test", Idempotency(nil), func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"created": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"name":"Vanilla"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestIdempotencyRejectsInvalidUUID(t *testing.T) {
	app := fiber.New()
	app.Post("/test", Idempotency(nil), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"name":"Vanilla"}`))
	req.Header.Set("Idempotency-Key", "not-a-valid-uuid")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var errResp map[string]string
	_ = json.Unmarshal(body, &errResp)
	if errResp["code"] != "INVALID_IDEMPOTENCY_KEY" {
		t.Fatalf("expected INVALID_IDEMPOTENCY_KEY, got %q", errResp["code"])
	}
}

func TestIdempotencyReplaysIdenticalRequest(t *testing.T) {
	app := fiber.New()
	var callCount int32
	app.Post("/api/v1/flavors", Idempotency(nil), func(c *fiber.Ctx) error {
		atomic.AddInt32(&callCount, 1)
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": "123", "name": "Vanilla"})
	})

	key := uuid.NewString()
	payload := `{"name":"Vanilla","price":5000}`

	// First request - executed by handler
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", bytes.NewBufferString(payload))
	req1.Header.Set("Idempotency-Key", key)
	req1.Header.Set("Content-Type", "application/json")

	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp1.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d", resp1.StatusCode)
	}
	body1, _ := io.ReadAll(resp1.Body)

	// Second request with same key and same payload - replayed from cache
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", bytes.NewBufferString(payload))
	req2.Header.Set("Idempotency-Key", key)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d", resp2.StatusCode)
	}
	body2, _ := io.ReadAll(resp2.Body)

	if string(body1) != string(body2) {
		t.Fatalf("expected identical response body, got %s vs %s", body1, body2)
	}
	if resp2.Header.Get("X-Idempotent-Replay") != "true" {
		t.Errorf("expected X-Idempotent-Replay header to be true")
	}

	// Verify the handler was called only once
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("expected handler called once, got %d", callCount)
	}
}

func TestIdempotencyReturns409OnConflictingPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/api/v1/flavors", Idempotency(nil), func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"created": true})
	})

	key := uuid.NewString()

	// First request with payload A
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", bytes.NewBufferString(`{"name":"Vanilla"}`))
	req1.Header.Set("Idempotency-Key", key)
	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp1.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d", resp1.StatusCode)
	}

	// Second request with different payload B using same key
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", bytes.NewBufferString(`{"name":"Chocolate"}`))
	req2.Header.Set("Idempotency-Key", key)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp2.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", resp2.StatusCode)
	}

	body, _ := io.ReadAll(resp2.Body)
	var errResp map[string]string
	_ = json.Unmarshal(body, &errResp)
	if errResp["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("expected IDEMPOTENCY_CONFLICT, got %q", errResp["code"])
	}
}

func TestMemoryIdempotencyStoreTTL(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	ctx := context.Background()

	record := &IdempotencyRecord{
		PayloadHash: "abc",
		StatusCode:  200,
		Body:        "{}",
	}

	err := store.Set(ctx, "test-key", record, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, "test-key")
	if err != nil || got == nil {
		t.Fatalf("expected found record, got err=%v", err)
	}

	time.Sleep(30 * time.Millisecond)
	gotExpired, err := store.Get(ctx, "test-key")
	if err != nil || gotExpired != nil {
		t.Fatalf("expected expired nil record, got %v", gotExpired)
	}
}
