package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

// Config holds every environment-driven setting the service needs.
type Config struct {
	Port        string
	RabbitMQURL string // optional: leave empty to run without a broker

	JWTSecret   string
	JWTIssuer   string
	JWTAudience string

	// OrderOwnerTTL is how long an order_id -> customer_id mapping learned from
	// order.placed is kept in memory so order.ready / order.cancelled can be routed.
	OrderOwnerTTL time.Duration
	// HeartbeatInterval is the SSE keep-alive comment interval.
	HeartbeatInterval time.Duration
	// SubscriberBuffer is the per-connection notification queue length.
	SubscriberBuffer int
}

// Load reads a .env file if present, then falls back to real env vars.
func Load() (Config, error) {
	_ = godotenv.Load() // ignore error: fine if .env doesn't exist (e.g. in prod)

	c := Config{
		Port:        getEnv("PORT", "8080"),
		RabbitMQURL: getEnv("RABBITMQ_URL", ""), // empty = messaging disabled
		JWTSecret:   os.Getenv("JWT_SECRET"),
		JWTIssuer:   getEnv("JWT_ISSUER", "gelatoflow-auth"),
		JWTAudience: getEnv("JWT_AUDIENCE", "gelatoflow-api"),
	}

	var err error
	if c.OrderOwnerTTL, err = getDuration("ORDER_OWNER_TTL", 24*time.Hour); err != nil {
		return c, err
	}
	if c.HeartbeatInterval, err = getDuration("HEARTBEAT_INTERVAL", 25*time.Second); err != nil {
		return c, err
	}
	c.SubscriberBuffer = 32

	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	return c, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 30s, got %q", key, raw)
	}
	return d, nil
}
