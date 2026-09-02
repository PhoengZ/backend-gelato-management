package middleware

import (
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
		parts := strings.Fields(c.Get(fiber.HeaderAuthorization))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return unauthorized(c)
		}

		claims, err := verifier.Parse(parts[1])
		if err != nil {
			return unauthorized(c)
		}
		if claims.Role != auth.RoleManager {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    "FORBIDDEN",
				"message": "Manager role is required",
			})
		}

		c.Locals(LocalUserID, claims.Subject)
		c.Locals(LocalUserRole, string(claims.Role))
		return c.Next()
	}
}

func unauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"code":    "UNAUTHORIZED",
		"message": "Authentication is required",
	})
}
