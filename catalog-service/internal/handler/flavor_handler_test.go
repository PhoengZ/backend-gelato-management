package handler

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"catalog-service/internal/middleware"
	"github.com/gofiber/fiber/v2"
)

func TestExtractActorIDFromJWTSub(t *testing.T) {
	app := fiber.New()
	var extracted string

	app.Get("/test", func(c *fiber.Ctx) error {
		c.Locals(middleware.LocalUserID, "user-jwt-12345")
		extracted = ExtractActorID(c)
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if extracted != "user-jwt-12345" {
		t.Errorf("expected user-jwt-12345, got %q", extracted)
	}
}

func TestExtractActorIDFromXUserIDHeader(t *testing.T) {
	app := fiber.New()
	var extracted string

	app.Get("/test", func(c *fiber.Ctx) error {
		extracted = ExtractActorID(c)
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "gateway-user-9988")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if extracted != "gateway-user-9988" {
		t.Errorf("expected gateway-user-9988, got %q", extracted)
	}
}

func TestExtractActorIDPriorityLocalsOverHeader(t *testing.T) {
	app := fiber.New()
	var extracted string

	app.Get("/test", func(c *fiber.Ctx) error {
		c.Locals(middleware.LocalUserID, "verified-jwt-actor")
		extracted = ExtractActorID(c)
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-ID", "spoofed-header-actor")
	_, _ = app.Test(req)

	if extracted != "verified-jwt-actor" {
		t.Errorf("expected verified-jwt-actor, got %q", extracted)
	}
}

func TestExtractActorIDFallbackAnonymous(t *testing.T) {
	app := fiber.New()
	var extracted string

	app.Get("/test", func(c *fiber.Ctx) error {
		extracted = ExtractActorID(c)
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	_, _ = app.Test(req)

	if extracted != "anonymous" {
		t.Errorf("expected anonymous, got %q", extracted)
	}
}

func TestLogAuditOutput(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.Writer())

	app := fiber.New()
	app.Post("/test", func(c *fiber.Ctx) error {
		c.Locals(middleware.LocalUserID, "manager-uuid-001")
		LogAudit(c, "CREATE", "flavor-uuid-777", "Pistachio")
		return c.SendStatus(fiber.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	_, _ = app.Test(req)

	output := buf.String()
	if !strings.Contains(output, "[AUDIT]") {
		t.Errorf("expected [AUDIT] in log, got %q", output)
	}
	if !strings.Contains(output, "actor_id=manager-uuid-001") {
		t.Errorf("expected actor_id=manager-uuid-001 in log, got %q", output)
	}
	if !strings.Contains(output, "resource_id=flavor-uuid-777") {
		t.Errorf("expected resource_id=flavor-uuid-777 in log, got %q", output)
	}
	if !strings.Contains(output, "action=CREATE") {
		t.Errorf("expected action=CREATE in log, got %q", output)
	}
}
