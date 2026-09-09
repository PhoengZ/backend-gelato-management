package middleware

import (
	"bytes"
	"context"
	"strings"
	"time"

	"catalog-service/internal/auth"

	"github.com/gofiber/fiber/v2"
)

const (
	LocalUserID   = "authenticated_user_id"
	LocalUserRole = "authenticated_user_role"
)

func RequestTimeout(duration time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), duration)
		defer cancel()
		c.SetUserContext(ctx)
		return c.Next()
	}
}

func RequireManager(verifier *auth.Verifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if allowed, err := authenticate(c, verifier, true); !allowed {
			return err
		}
		if c.Locals(LocalUserRole) != string(auth.RoleManager) {
			return ManagerRequired(c)
		}
		return c.Next()
	}
}

// AuthenticateIfPresent permits anonymous active-catalog reads. An explicitly
// supplied but invalid token fails closed rather than becoming anonymous.
func AuthenticateIfPresent(verifier *auth.Verifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if allowed, err := authenticate(c, verifier, false); !allowed {
			return err
		}
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
	parts := strings.Fields(c.Get(fiber.HeaderAuthorization))
	if count != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
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

// ManagerRequired also distinguishes a missing identity from a lower role.
func ManagerRequired(c *fiber.Ctx) error {
	if c.Locals(LocalUserID) == nil || c.Locals(LocalUserID) == "" {
		return unauthorized(c)
	}
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"code": "FORBIDDEN", "message": "Manager role is required",
	})
}

func PreventCaching(c *fiber.Ctx) error {
	// Public and Manager representations share URLs, and archive changes visibility.
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Vary(fiber.HeaderAuthorization)
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	return c.Next()
}

func unauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"code":    "UNAUTHORIZED",
		"message": "Authentication is required",
	})
}
