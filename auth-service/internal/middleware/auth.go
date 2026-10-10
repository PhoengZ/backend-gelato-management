package middleware

import (
	"bytes"
	"strings"

	"auth-service/internal/auth"

	"github.com/gofiber/fiber/v2"
)

const (
	LocalUserID   = "authenticated_user_id"
	LocalUserRole = "authenticated_user_role"
)

func RequireAccessToken(tokens *auth.TokenManager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals(LocalUserID, "")
		c.Locals(LocalUserRole, "")
		count := 0
		c.Request().Header.VisitAll(func(key, _ []byte) {
			if bytes.EqualFold(key, []byte(fiber.HeaderAuthorization)) {
				count++
			}
		})
		parts := strings.Fields(c.Get(fiber.HeaderAuthorization))
		if count != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return unauthorized(c)
		}

		claims, err := tokens.Parse(parts[1])
		if err != nil {
			return unauthorized(c)
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
