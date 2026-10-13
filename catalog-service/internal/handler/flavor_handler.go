package handler

import (
	"log"
	"strings"

	"catalog-service/internal/middleware"
	"github.com/gofiber/fiber/v2"
)

// ExtractActorID extracts the authenticated user ID from JWT claims (stored in c.Locals)
// or from the X-User-ID request header for auditability (NFR-08).
func ExtractActorID(c *fiber.Ctx) string {
	if val, ok := c.Locals(middleware.LocalUserID).(string); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	if headerVal := strings.TrimSpace(c.Get("X-User-ID")); headerVal != "" {
		return headerVal
	}
	return "anonymous"
}

// LogAudit emits a structured audit record for flavor mutations (NFR-08).
func LogAudit(c *fiber.Ctx, action, flavorID, details string) {
	actorID := ExtractActorID(c)
	log.Printf("[AUDIT] action=%s actor_id=%s resource=flavor resource_id=%s details=%s",
		action, actorID, flavorID, details,
	)
}
