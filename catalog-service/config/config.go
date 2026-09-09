package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const minimumJWTSecretBytes = 32

type Config struct {
	Port           string
	RedisURL       string
	RedisKeyPrefix string
	JWTSecret      string
	JWTIssuer      string
	JWTAudience    string
	RequestTimeout time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		Port:           envOrDefault("PORT", "3002"),
		RedisURL:       os.Getenv("REDIS_URL"),
		RedisKeyPrefix: envOrDefault("REDIS_KEY_PREFIX", "catalog:v1"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		JWTIssuer:      envOrDefault("JWT_ISSUER", "gelatoflow-auth"),
		JWTAudience:    envOrDefault("JWT_AUDIENCE", "gelatoflow-api"),
	}

	if err := validateRedisURL(cfg.RedisURL); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.RedisKeyPrefix) == "" || strings.ContainsAny(cfg.RedisKeyPrefix, " \t\r\n") {
		return Config{}, fmt.Errorf("REDIS_KEY_PREFIX must be non-empty and contain no whitespace")
	}
	if len([]byte(cfg.JWTSecret)) < minimumJWTSecretBytes {
		return Config{}, fmt.Errorf("JWT_SECRET must contain at least %d bytes", minimumJWTSecretBytes)
	}

	timeout, err := time.ParseDuration(envOrDefault("REQUEST_TIMEOUT", "3s"))
	if err != nil || timeout <= 0 {
		return Config{}, fmt.Errorf("REQUEST_TIMEOUT must be a positive duration")
	}
	cfg.RequestTimeout = timeout

	return cfg, nil
}

func validateRedisURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("REDIS_URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") || parsed.Host == "" {
		return fmt.Errorf("REDIS_URL must be a valid redis or rediss URL")
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
