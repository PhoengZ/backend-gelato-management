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

// RateLimiter tracks failed authentication attempts keyed by client IP and account email
// to protect sensitive credential endpoints (/login, /signup) against brute-force attacks.
type RateLimiter struct {
	mu           sync.RWMutex
	maxFailures  int
	window       time.Duration
	ipFailures   map[string]*failureRecord
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
		ipFailures:   make(map[string]*failureRecord),
		acctFailures: make(map[string]*failureRecord),
		now:          time.Now,
	}
}

// IsBlocked reports whether the specified IP or account has exceeded max failed attempts.
// Returns (true, retryAfterSeconds) if blocked, or (false, 0) if allowed.
func (l *RateLimiter) IsBlocked(ip, account string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanupExpired(now)

	// Check IP failure record
	if ip != "" {
		if rec, exists := l.ipFailures[ip]; exists && rec.count >= l.maxFailures && now.Before(rec.expiresAt) {
			retry := int(rec.expiresAt.Sub(now).Seconds()) + 1
			if retry < 1 {
				retry = 1
			}
			return true, retry
		}
	}

	// Check account failure record
	accountKey := strings.ToLower(strings.TrimSpace(account))
	if accountKey != "" {
		if rec, exists := l.acctFailures[accountKey]; exists && rec.count >= l.maxFailures && now.Before(rec.expiresAt) {
			retry := int(rec.expiresAt.Sub(now).Seconds()) + 1
			if retry < 1 {
				retry = 1
			}
			return true, retry
		}
	}

	return false, 0
}

// RecordFailure registers a failed authentication attempt for the IP and account.
func (l *RateLimiter) RecordFailure(ip, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanupExpired(now)

	if ip != "" {
		rec, exists := l.ipFailures[ip]
		if !exists || !now.Before(rec.expiresAt) {
			rec = &failureRecord{expiresAt: now.Add(l.window)}
			l.ipFailures[ip] = rec
		}
		rec.count++
	}

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

// Reset clears failure counters for the given IP and account upon successful authentication.
func (l *RateLimiter) Reset(ip, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ip != "" {
		delete(l.ipFailures, ip)
	}

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

// Middleware creates a Fiber handler that verifies the client IP against brute-force limits.
func (l *RateLimiter) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		return l.Check(c, "")
	}
}

func (l *RateLimiter) cleanupExpired(now time.Time) {
	for k, v := range l.ipFailures {
		if !now.Before(v.expiresAt) {
			delete(l.ipFailures, k)
		}
	}
	for k, v := range l.acctFailures {
		if !now.Before(v.expiresAt) {
			delete(l.acctFailures, k)
		}
	}
}

func extractClientIP(c *fiber.Ctx) string {
	if rip := c.Context().RemoteIP(); len(rip) > 0 {
		return rip.String()
	}
	return c.IP()
}
