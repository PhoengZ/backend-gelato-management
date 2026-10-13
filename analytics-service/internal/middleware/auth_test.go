package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"analytics-service/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testSecret   = "test_secret_that_is_longer_than_32_bytes"
	testIssuer   = "gelatoflow-auth"
	testAudience = "gelatoflow-api"
)

func createTestToken(t *testing.T, secret string, role Role, issuer, audience string, ttl time.Duration) string {
	t.Helper()
	now := time.Now().UTC()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "a3b8c4d2-1234-4567-89ab-cdef01234567",
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("Failed to sign test token: %v", err)
	}
	return signed
}

func setupAuthTestApp(verifier *Verifier) *fiber.App {
	app := fiber.New()
	api := app.Group("/api/v1/analytics", RequireManager(verifier))
	api.Get("/summary", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"user_id": c.Locals(LocalUserID),
			"role":    c.Locals(LocalUserRole),
		})
	})
	return app
}

func TestRequireManager(t *testing.T) {
	verifier := NewVerifier(testSecret, testIssuer, testAudience)
	app := setupAuthTestApp(verifier)

	t.Run("valid manager token succeeds", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleManager, testIssuer, testAudience, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected status 200, got %d", resp.StatusCode)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if body["role"] != "MANAGER" {
			t.Errorf("Expected role MANAGER, got %v", body["role"])
		}
	})

	t.Run("staff role rejected with 403 forbidden", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleStaff, testIssuer, testAudience, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected status 403 Forbidden, got %d", resp.StatusCode)
		}

		var errResp models.ApiErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if errResp.Code != "FORBIDDEN" {
			t.Errorf("Expected code FORBIDDEN, got %s", errResp.Code)
		}
	})

	t.Run("customer role rejected with 403 forbidden", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleCustomer, testIssuer, testAudience, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("Expected status 403 Forbidden, got %d", resp.StatusCode)
		}

		var errResp models.ApiErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if errResp.Code != "FORBIDDEN" {
			t.Errorf("Expected code FORBIDDEN, got %s", errResp.Code)
		}
	})

	t.Run("missing authorization header rejected with 401 unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}

		var errResp models.ApiErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			t.Fatalf("Decode failed: %v", err)
		}
		if errResp.Code != "UNAUTHORIZED" {
			t.Errorf("Expected code UNAUTHORIZED, got %s", errResp.Code)
		}
	})

	t.Run("non-bearer authorization header rejected with 401 unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Basic user:pass")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}
	})

	t.Run("duplicate authorization headers rejected with 401 unauthorized", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleManager, testIssuer, testAudience, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Add("Authorization", "Bearer "+token)
		req.Header.Add("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}
	})

	t.Run("expired token rejected with 401 unauthorized", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleManager, testIssuer, testAudience, -10*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}
	})

	t.Run("wrong signing key rejected with 401 unauthorized", func(t *testing.T) {
		token := createTestToken(t, "another_secret_key_32_bytes_long_12345", RoleManager, testIssuer, testAudience, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}
	})

	t.Run("mismatched issuer rejected with 401 unauthorized", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleManager, "wrong-issuer", testAudience, 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}
	})

	t.Run("mismatched audience rejected with 401 unauthorized", func(t *testing.T) {
		token := createTestToken(t, testSecret, RoleManager, testIssuer, "wrong-audience", 15*time.Minute)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Expected status 401 Unauthorized, got %d", resp.StatusCode)
		}
	})
}
