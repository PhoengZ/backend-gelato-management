package middleware

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	DefaultMaxFailedAttempts = 5
	DefaultRateLimitWindow   = 15 * time.Minute
)

type failureRecord struct {
	count     int
	expiresAt time.Time
}

// RateLimiter tracks bad credentials by canonical account, independently of the
// TCP peer. CredentialLimiter owns transport admission for shared Gateway peers.
type RateLimiter struct {
	mu           sync.RWMutex
	maxFailures  int
	window       time.Duration
	acctFailures map[string]*failureRecord
	now          func() time.Time
}

func NewRateLimiter(maxFailures int, window time.Duration) *RateLimiter {
	if maxFailures <= 0 {
		maxFailures = DefaultMaxFailedAttempts
	}
	if window <= 0 {
		window = DefaultRateLimitWindow
	}
	return &RateLimiter{
		maxFailures:  maxFailures,
		window:       window,
		acctFailures: make(map[string]*failureRecord),
		now:          time.Now,
	}
}

// IsBlocked reports whether this account exceeded the failed-credential budget.
// The peer argument is retained for internal caller compatibility, never keyed.
// Returns (true, retryAfterSeconds) if blocked, or (false, 0) if allowed.
func (l *RateLimiter) IsBlocked(_ string, account string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanupExpired(now)

	// Check account failure record
	accountKey := strings.ToLower(strings.TrimSpace(account))
	if accountKey != "" {
		if rec, exists := l.acctFailures[accountKey]; exists && rec.count >= l.maxFailures && now.Before(rec.expiresAt) {
			retry := int((rec.expiresAt.Sub(now) + time.Second - 1) / time.Second)
			if retry < 1 {
				retry = 1
			}
			return true, retry
		}
	}

	return false, 0
}

// RecordFailure registers only a confirmed invalid-credentials result.
func (l *RateLimiter) RecordFailure(_ string, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanupExpired(now)

	accountKey := strings.ToLower(strings.TrimSpace(account))
	if accountKey != "" {
		rec, exists := l.acctFailures[accountKey]
		if !exists || !now.Before(rec.expiresAt) {
			rec = &failureRecord{expiresAt: now.Add(l.window)}
			l.acctFailures[accountKey] = rec
		}
		rec.count++
	}
}

// Reset clears this account only; success never resets another account's budget.
func (l *RateLimiter) Reset(_ string, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	accountKey := strings.ToLower(strings.TrimSpace(account))
	if accountKey != "" {
		delete(l.acctFailures, accountKey)
	}
}

// Check inspects the incoming request context and account key. If rate limited, it sends 429.
func (l *RateLimiter) Check(c *fiber.Ctx, account string) error {
	ip := extractClientIP(c)
	blocked, retry := l.IsBlocked(ip, account)
	if blocked {
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retry))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"code":    "RATE_LIMITED",
			"message": "Too many failed attempts. Please try again later.",
		})
	}
	return nil
}

// Middleware requires an explicit account key; it never creates an IP lockout.
func (l *RateLimiter) Middleware(account string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return l.Check(c, account)
	}
}

func (l *RateLimiter) cleanupExpired(now time.Time) {
	for k, v := range l.acctFailures {
		if !now.Before(v.expiresAt) {
			delete(l.acctFailures, k)
		}
	}
}

func extractClientIP(c *fiber.Ctx) string {
	return PeerIP(c)
}

// PeerIP is deliberately the network peer. Forwarding headers are not trusted;
// a Gateway connection is identified as a Gateway, not as the end user's IP.
func PeerIP(c *fiber.Ctx) string {
	if rip := c.Context().RemoteIP(); len(rip) > 0 {
		return rip.String()
	}
	return "unknown"
}
