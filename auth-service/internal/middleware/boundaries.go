package middleware

import (
	"bytes"
	"context"
	"mime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

const CredentialBodyLimit = 16 << 10

func PreventCaching(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderPragma, "no-cache")
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Vary(fiber.HeaderAuthorization)
	return c.Next()
}

func RequestTimeout(duration time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), duration)
		defer cancel()
		c.SetUserContext(ctx)
		return c.Next()
	}
}

// Reject compression before any Fiber Body call can decompress attacker input.
func RequireCredentialJSON(c *fiber.Ctx) error {
	contentTypes, encodings := 0, 0
	c.Request().Header.VisitAll(func(key, _ []byte) {
		if bytes.EqualFold(key, []byte(fiber.HeaderContentType)) {
			contentTypes++
		}
		if bytes.EqualFold(key, []byte(fiber.HeaderContentEncoding)) {
			encodings++
		}
	})
	mediaType, params, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	encoding := strings.TrimSpace(c.Get(fiber.HeaderContentEncoding))
	if contentTypes != 1 || err != nil || mediaType != "application/json" ||
		(params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) ||
		encodings > 1 || (encoding != "" && !strings.EqualFold(encoding, "identity")) {
		return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"code": "UNSUPPORTED_MEDIA_TYPE", "message": "Uncompressed application/json is required"})
	}
	if len(c.Request().Body()) > CredentialBodyLimit {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"code": "REQUEST_TOO_LARGE", "message": "Request body exceeds 16 KiB"})
	}
	return c.Next()
}

type rateWindow struct {
	count   int
	expires time.Time
}

// rateBudget is bounded and expires inactive identities instead of allowing
// arbitrary client input to grow process memory without limit.
type rateBudget struct {
	mu             sync.Mutex
	windows        map[string]rateWindow
	now            func() time.Time
	limit, maxKeys int
	window         time.Duration
}

func newRateBudget(limit, maxKeys int) *rateBudget {
	return &rateBudget{windows: make(map[string]rateWindow), now: time.Now, limit: limit, maxKeys: maxKeys, window: time.Minute}
}

func (l *rateBudget) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for key, entry := range l.windows {
		if !now.Before(entry.expires) {
			delete(l.windows, key)
		}
	}
	entry, exists := l.windows[key]
	if !exists {
		if len(l.windows) >= l.maxKeys {
			return false, int(l.window / time.Second)
		}
		entry.expires = now.Add(l.window)
	}
	if entry.count >= l.limit {
		return false, int((entry.expires.Sub(now) + time.Second - 1) / time.Second)
	}
	entry.count++
	l.windows[key] = entry
	return true, 0
}

// CredentialLimiter separates a generous transport abuse limit from the
// account budget, so one shared BFF peer does not become one login identity.
// Every instance is shared by signup and login for the lifetime of the router.
type CredentialLimiter struct {
	transport *rateBudget
	accounts  *rateBudget
	work      chan struct{}
}

func NewCredentialLimiter() *CredentialLimiter {
	return &CredentialLimiter{
		transport: newRateBudget(600, 2048),
		accounts:  newRateBudget(20, 4096),
		work:      make(chan struct{}, 4),
	}
}

// Handle counts even malformed attempts before JSON parsing. Only the actual
// TCP peer is used; forwarding headers do not grant a fresh transport budget.
func (l *CredentialLimiter) Handle(c *fiber.Ctx) error {
	allowed, retry := l.transport.allow(c.Context().RemoteIP().String())
	if !allowed {
		return RateLimited(c, retry)
	}
	return c.Next()
}

// Acquire takes a canonical, validated account email, independent of peer IP.
// Call only after strict body validation and service.NormalizeEmail. The caller
// holds the returned slot until credential work completes. Busy responses do
// not consume an account attempt; no goroutines wait on the work semaphore.
func (l *CredentialLimiter) Acquire(email string) (release func(), retryAfter int) {
	select {
	case l.work <- struct{}{}:
	default:
		return nil, 1
	}
	if allowed, retry := l.accounts.allow(email); !allowed {
		<-l.work
		return nil, retry
	}
	return func() { <-l.work }, 0
}

func RateLimited(c *fiber.Ctx, retry int) error {
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retry))
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"code": "RATE_LIMITED", "message": "Too many authentication requests"})
}
