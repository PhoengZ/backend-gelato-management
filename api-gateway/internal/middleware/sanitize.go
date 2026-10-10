package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// StripIdentityHeaders removes any spoofed identity headers provided by clients
// before the request is routed downstream.
func StripIdentityHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		var toDelete [][]byte

		c.Request().Header.VisitAll(func(key, _ []byte) {
			lower := strings.ToLower(string(key))
			if strings.HasPrefix(lower, "x-user-") ||
				strings.HasPrefix(lower, "x-authenticated-") ||
				lower == "x-role" ||
				lower == "x-roles" {
				toDelete = append(toDelete, append([]byte(nil), key...))
			}
		})

		for _, key := range toDelete {
			c.Request().Header.DelBytes(key)
		}

		return c.Next()
	}
}
