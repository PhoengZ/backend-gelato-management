package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"catalog-service/internal/idempotency"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const IdempotencyHeaderKey = "Idempotency-Key"

// Idempotency validates request metadata only. The repository commits the key,
// hash, original response, flavor and audit in one Redis operation. There is no
// post-handler cache SET and no unsafe in-memory/runtime fallback.
func Idempotency() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Method() != fiber.MethodPost && c.Method() != fiber.MethodPut && c.Method() != fiber.MethodPatch && c.Method() != fiber.MethodDelete {
			return c.Next()
		}
		count := 0
		c.Request().Header.VisitAll(func(key, _ []byte) {
			if bytes.EqualFold(key, []byte(IdempotencyHeaderKey)) {
				count++
			}
		})
		raw := strings.TrimSpace(c.Get(IdempotencyHeaderKey))
		if count == 0 || (count == 1 && raw == "") {
			return c.Next()
		}
		key, err := uuid.Parse(raw)
		if count != 1 || err != nil {
			return c.Status(400).JSON(fiber.Map{"code": "INVALID_IDEMPOTENCY_KEY", "message": "Provide exactly one UUID Idempotency-Key"})
		}
		hash := sha256.Sum256(c.Body())
		request := idempotency.Request{Key: key, RawKey: raw, PayloadHash: hex.EncodeToString(hash[:])}
		if c.Method() != fiber.MethodPost {
			id, err := uuid.Parse(c.Params("flavor_id"))
			if err != nil || id == uuid.Nil || key == uuid.Nil {
				return c.Status(400).JSON(fiber.Map{"code": "INVALID_ARGUMENT", "message": "Provide non-nil resource and idempotency UUIDs"})
			}
			request.Scope = c.Method() + ":" + id.String()
		}
		c.SetUserContext(idempotency.WithRequest(c.UserContext(), request))
		return c.Next()
	}
}
