package middleware

import (
	"bytes"
	"strings"

	"github.com/gelato/api-gateway/internal/auth"
	"github.com/gofiber/fiber/v2"
)

const (
	LocalUserID   = "authenticated_user_id"
	LocalUserRole = "authenticated_user_role"
)

// RequireAuth enforces that a valid Bearer JWT is present in the request.
func RequireAuth(verifier *auth.Verifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if allowed, err := authenticate(c, verifier, true); !allowed {
			return err
		}
		InjectUserHeaders(c)
		return c.Next()
	}
}

// AuthenticateIfPresent allows unauthenticated requests to proceed, but if an
// Authorization header is supplied, it must be valid (fails closed).
func AuthenticateIfPresent(verifier *auth.Verifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if allowed, err := authenticate(c, verifier, false); !allowed {
			return err
		}
		InjectUserHeaders(c)
		return c.Next()
	}
}

func authenticate(c *fiber.Ctx, verifier *auth.Verifier, required bool) (bool, error) {
	c.Locals(LocalUserID, "")
	c.Locals(LocalUserRole, "")

	count := 0
	c.Request().Header.VisitAll(func(key, _ []byte) {
		if bytes.EqualFold(key, []byte(fiber.HeaderAuthorization)) {
			count++
		}
	})

	if count == 0 && !required {
		return true, nil
	}

	if count != 1 {
		return false, unauthorized(c)
	}

	parts := strings.Fields(c.Get(fiber.HeaderAuthorization))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false, unauthorized(c)
	}

	claims, err := verifier.Parse(parts[1])
	if err != nil {
		return false, unauthorized(c)
	}

	c.Locals(LocalUserID, claims.Subject)
	c.Locals(LocalUserRole, string(claims.Role))
	return true, nil
}

func unauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"code":    "UNAUTHORIZED",
		"message": "Authentication is required",
	})
}
