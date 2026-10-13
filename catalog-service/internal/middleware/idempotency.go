package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	IdempotencyHeaderKey  = "Idempotency-Key"
	DefaultIdempotencyTTL = 24 * time.Hour
)

// IdempotencyRecord stores the cached response state and payload hash.
type IdempotencyRecord struct {
	PayloadHash string            `json:"payload_hash"`
	StatusCode  int               `json:"status_code"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
}

// IdempotencyStore defines the storage interface for idempotency records.
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (*IdempotencyRecord, error)
	Set(ctx context.Context, key string, record *IdempotencyRecord, ttl time.Duration) error
}

// RedisIdempotencyStore implements IdempotencyStore backed by Redis.
type RedisIdempotencyStore struct {
	client redis.Cmdable
	prefix string
}

func NewRedisIdempotencyStore(client redis.Cmdable, prefix ...string) *RedisIdempotencyStore {
	p := "idempotency:"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
		if !strings.HasSuffix(p, ":") {
			p += ":"
		}
	}
	return &RedisIdempotencyStore{
		client: client,
		prefix: p,
	}
}

func (s *RedisIdempotencyStore) Get(ctx context.Context, key string) (*IdempotencyRecord, error) {
	val, err := s.client.Get(ctx, s.prefix+key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	var record IdempotencyRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *RedisIdempotencyStore) Set(ctx context.Context, key string, record *IdempotencyRecord, ttl time.Duration) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.prefix+key, data, ttl).Err()
}

type memoryEntry struct {
	record    *IdempotencyRecord
	expiresAt time.Time
}

// MemoryIdempotencyStore provides an in-memory thread-safe store for testing and fallback.
type MemoryIdempotencyStore struct {
	mu      sync.RWMutex
	entries map[string]memoryEntry
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{
		entries: make(map[string]memoryEntry),
	}
}

func (m *MemoryIdempotencyStore) Get(_ context.Context, key string) (*IdempotencyRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, found := m.entries[key]
	if !found {
		return nil, nil
	}
	if time.Now().After(entry.expiresAt) {
		return nil, nil
	}
	return entry.record, nil
}

func (m *MemoryIdempotencyStore) Set(_ context.Context, key string, record *IdempotencyRecord, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[key] = memoryEntry{
		record:    record,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

// NewIdempotency creates an idempotency middleware using the provided IdempotencyStore.
func NewIdempotency(store IdempotencyStore) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Only mutate requests support idempotency
		if c.Method() != fiber.MethodPost {
			return c.Next()
		}

		rawKey := strings.TrimSpace(c.Get(IdempotencyHeaderKey))
		if rawKey == "" {
			// Idempotency key is optional unless provided
			return c.Next()
		}

		// Validate UUID format
		if _, err := uuid.Parse(rawKey); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"code":    "INVALID_IDEMPOTENCY_KEY",
				"message": "Idempotency-Key must be a valid UUID",
			})
		}

		// Calculate SHA-256 of request payload
		body := c.Body()
		hash := sha256.Sum256(body)
		payloadHash := hex.EncodeToString(hash[:])

		ctx := c.UserContext()
		if ctx == nil {
			ctx = context.Background()
		}

		// Check existing record
		existing, err := store.Get(ctx, rawKey)
		if err != nil {
			// If store read fails, log and continue to avoid outage
			return c.Next()
		}

		if existing != nil {
			// Duplicate key with different payload -> 409 Conflict
			if existing.PayloadHash != payloadHash {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{
					"code":    "IDEMPOTENCY_CONFLICT",
					"message": "Idempotency key already used with different payload",
				})
			}

			// Duplicate key with identical payload -> replay cached response
			for k, v := range existing.Headers {
				c.Set(k, v)
			}
			c.Set("X-Idempotent-Replay", "true")
			return c.Status(existing.StatusCode).Send([]byte(existing.Body))
		}

		// Proceed with handler execution
		if err := c.Next(); err != nil {
			return err
		}

		statusCode := c.Response().StatusCode()
		// Only cache successful mutation responses
		if statusCode >= fiber.StatusOK && statusCode < fiber.StatusMultipleChoices {
			respBody := string(c.Response().Body())
			headers := make(map[string]string)
			headers[fiber.HeaderContentType] = string(c.Response().Header.ContentType())

			record := &IdempotencyRecord{
				PayloadHash: payloadHash,
				StatusCode:  statusCode,
				Headers:     headers,
				Body:        respBody,
			}

			_ = store.Set(ctx, rawKey, record, DefaultIdempotencyTTL)
		}

		return nil
	}
}

// Idempotency returns a Fiber middleware configured with Redis (or in-memory store if client is nil).
func Idempotency(redisClient redis.Cmdable) fiber.Handler {
	if redisClient != nil {
		return NewIdempotency(NewRedisIdempotencyStore(redisClient))
	}
	return NewIdempotency(NewMemoryIdempotencyStore())
}
