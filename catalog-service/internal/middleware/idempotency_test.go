package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"

	"catalog-service/internal/idempotency"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestIdempotencyBindsCanonicalRequestWithoutCachingSideEffects(t *testing.T) {
	key := uuid.New()
	for _, raw := range []string{"", key.String(), strings.ToUpper(key.String()), key.URN(), strings.ReplaceAll(key.String(), "-", "")} {
		app := fiber.New()
		app.Post("/test", Idempotency(), func(c *fiber.Ctx) error {
			request, present := idempotency.FromContext(c.UserContext())
			if raw == "" {
				if present {
					t.Fatal("optional absent key bound")
				}
			} else {
				hash := sha256.Sum256([]byte(`{"name":"Vanilla"}`))
				if !present || request.Key != key || request.PayloadHash != hex.EncodeToString(hash[:]) {
					t.Fatalf("invalid canonical request: %+v", request)
				}
			}
			return c.SendStatus(201)
		})
		req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"name":"Vanilla"}`))
		if raw != "" {
			req.Header.Set("Idempotency-Key", raw)
		}
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 201 {
			t.Fatal(response.StatusCode)
		}
	}
}

func TestIdempotencyRejectsMalformedAndDuplicateHeaders(t *testing.T) {
	for _, values := range [][]string{{"not-a-uuid"}, {uuid.NewString(), uuid.NewString()}} {
		app := fiber.New()
		app.Post("/test", Idempotency(), func(c *fiber.Ctx) error { return c.SendStatus(201) })
		req := httptest.NewRequest("POST", "/test", nil)
		for _, value := range values {
			req.Header.Add("Idempotency-Key", value)
		}
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("ambiguous key accepted: %d", response.StatusCode)
		}
	}
}
