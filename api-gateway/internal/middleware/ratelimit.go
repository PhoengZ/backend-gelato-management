package middleware

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

const (
	DefaultPublicRateLimit = 100
	DefaultAuthRateLimit   = 300
	DefaultRateLimitWindow = 1 * time.Minute
)

// RateLimiterConfig customizes the rate limiter behavior.
type RateLimiterConfig struct {
	Max          int
	Expiration   time.Duration
	KeyGenerator func(*fiber.Ctx) string
	Storage      fiber.Storage
}

// PublicRateLimiter creates a rate limiter for public and auth endpoints (100 req/min by default).
// Keyed by client IP. Returns structured JSON error on 429 Too Many Requests.
func PublicRateLimiter(configs ...RateLimiterConfig) fiber.Handler {
	cfg := RateLimiterConfig{
		Max:        DefaultPublicRateLimit,
		Expiration: DefaultRateLimitWindow,
	}
	if len(configs) > 0 {
		if configs[0].Max > 0 {
			cfg.Max = configs[0].Max
		}
		if configs[0].Expiration > 0 {
			cfg.Expiration = configs[0].Expiration
		}
		if configs[0].KeyGenerator != nil {
			cfg.KeyGenerator = configs[0].KeyGenerator
		}
		if configs[0].Storage != nil {
			cfg.Storage = configs[0].Storage
		}
	}

	keyGen := cfg.KeyGenerator
	if keyGen == nil {
		keyGen = func(c *fiber.Ctx) string {
			return c.IP()
		}
	}

	return limiter.New(limiter.Config{
		Max:          cfg.Max,
		Expiration:   cfg.Expiration,
		KeyGenerator: keyGen,
		Storage:      cfg.Storage,
		LimitReached: rateLimitReached,
	})
}

// AuthenticatedRateLimiter creates a rate limiter for authenticated endpoints (300 req/min by default).
// Keyed by authenticated user ID (from c.Locals), falling back to client IP.
// Returns structured JSON error on 429 Too Many Requests.
func AuthenticatedRateLimiter(configs ...RateLimiterConfig) fiber.Handler {
	cfg := RateLimiterConfig{
		Max:        DefaultAuthRateLimit,
		Expiration: DefaultRateLimitWindow,
	}
	if len(configs) > 0 {
		if configs[0].Max > 0 {
			cfg.Max = configs[0].Max
		}
		if configs[0].Expiration > 0 {
			cfg.Expiration = configs[0].Expiration
		}
		if configs[0].KeyGenerator != nil {
			cfg.KeyGenerator = configs[0].KeyGenerator
		}
		if configs[0].Storage != nil {
			cfg.Storage = configs[0].Storage
		}
	}

	keyGen := cfg.KeyGenerator
	if keyGen == nil {
		keyGen = func(c *fiber.Ctx) string {
			if uid, ok := c.Locals(LocalUserID).(string); ok && uid != "" {
				return "user:" + uid
			}
			return "ip:" + c.IP()
		}
	}

	return limiter.New(limiter.Config{
		Max:          cfg.Max,
		Expiration:   cfg.Expiration,
		KeyGenerator: keyGen,
		Storage:      cfg.Storage,
		LimitReached: rateLimitReached,
	})
}

func rateLimitReached(c *fiber.Ctx) error {
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"code":    "RATE_LIMITED",
		"message": "Too many requests",
	})
}
