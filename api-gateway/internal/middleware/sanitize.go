package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Trusted downstream header names.
const (
	HeaderXUserID   = "X-User-ID"
	HeaderXUserRole = "X-User-Role"
)

// StripIdentityHeaders removes any spoofed identity headers provided by clients
// before the request is routed downstream.
func StripIdentityHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		var toDelete [][]byte

		c.Request().Header.VisitAll(func(key, _ []byte) {
			lower := strings.ToLower(string(key))
			if strings.HasPrefix(lower, "x-user") ||
				strings.HasPrefix(lower, "x-role") ||
				strings.HasPrefix(lower, "x-authenticated-") {
				toDelete = append(toDelete, append([]byte(nil), key...))
			}
		})

		for _, key := range toDelete {
			c.Request().Header.DelBytes(key)
		}

		return c.Next()
	}
}

// InjectTrustedHeaders injects trusted downstream headers from verified JWT claims
// stored in c.Locals into the request headers.
func InjectTrustedHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		InjectUserHeaders(c)
		return c.Next()
	}
}

// InjectUserHeaders injects X-User-ID and X-User-Role into the downstream request header
// from verified JWT claims stored in c.Locals.
func InjectUserHeaders(c *fiber.Ctx) {
	if userID, ok := c.Locals(LocalUserID).(string); ok && userID != "" {
		c.Request().Header.Set(HeaderXUserID, userID)
	}
	if userRole, ok := c.Locals(LocalUserRole).(string); ok && userRole != "" {
		c.Request().Header.Set(HeaderXUserRole, userRole)
	}
}
