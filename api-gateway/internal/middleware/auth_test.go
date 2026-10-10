package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gelato/api-gateway/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test_secret_that_is_longer_than_32_bytes"

func validToken(t *testing.T, role auth.Role) string {
	t.Helper()
	now := time.Now().UTC()
	claims := auth.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "a3b8c4d2-1234-4567-89ab-cdef01234567",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

func TestStripIdentityHeaders(t *testing.T) {
	app := fiber.New()
	app.Use(StripIdentityHeaders())

	var capturedHeaders http.Header
	app.Get("/test", func(c *fiber.Ctx) error {
		capturedHeaders = make(http.Header)
		c.Request().Header.VisitAll(func(key, val []byte) {
			capturedHeaders.Add(string(key), string(val))
		})
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-User-Id", "malicious-user-id")
	req.Header.Set("X-User-Role", "MANAGER")
	req.Header.Set("X-User-Email", "attacker@example.com")
	req.Header.Set("X-Role", "ADMIN")
	req.Header.Set("X-Roles", "SUPERUSER")
	req.Header.Set("X-Authenticated-User-Id", "spoofed-id")
	req.Header.Set("Authorization", "Bearer legitimate-token")
	req.Header.Set("Custom-Header", "allowed-value")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	for _, stripped := range []string{
		"X-User-Id", "X-User-Role", "X-User-Email",
		"X-Role", "X-Roles", "X-Authenticated-User-Id",
	} {
		if val := capturedHeaders.Get(stripped); val != "" {
			t.Errorf("header %s was not stripped, got %q", stripped, val)
		}
	}

	if val := capturedHeaders.Get("Authorization"); val != "Bearer legitimate-token" {
		t.Errorf("Authorization header altered or missing: %q", val)
	}
	if val := capturedHeaders.Get("Custom-Header"); val != "allowed-value" {
		t.Errorf("Custom-Header altered or missing: %q", val)
	}
}

func TestRequireAuth(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, "gelatoflow-auth", "gelatoflow-api")

	app := fiber.New()
	app.Get("/protected", RequireAuth(verifier), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"user_id": c.Locals(LocalUserID),
			"role":    c.Locals(LocalUserRole),
		})
	})

	t.Run("missing authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("invalid authorization scheme", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Basic user:pass")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("duplicate authorization headers", func(t *testing.T) {
		token := validToken(t, auth.RoleCustomer)
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Add("Authorization", "Bearer "+token)
		req.Header.Add("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer invalid.token.value")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("valid token succeeds", func(t *testing.T) {
		token := validToken(t, auth.RoleManager)
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})
}

func TestAuthenticateIfPresent(t *testing.T) {
	verifier := auth.NewVerifier(testSecret, "gelatoflow-auth", "gelatoflow-api")

	app := fiber.New()
	app.Get("/optional", AuthenticateIfPresent(verifier), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"user_id": c.Locals(LocalUserID),
			"role":    c.Locals(LocalUserRole),
		})
	})

	t.Run("anonymous request succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/optional", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("invalid token fails closed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/optional", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("valid token succeeds", func(t *testing.T) {
		token := validToken(t, auth.RoleStaff)
		req := httptest.NewRequest(http.MethodGet, "/optional", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})
}
