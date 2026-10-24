package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// MinimumJWTSecretBytes matches the HS256 secret length the API Gateway requires.
const MinimumJWTSecretBytes = 32

// Config holds every environment-driven setting the service needs.
type Config struct {
	Port        string
	DatabaseURL string
	RabbitMQURL string // optional: leave empty to run without a broker
	JWTSecret   string // required: no default so a shared, public secret is never used
	JWTIssuer   string
	JWTAudience string
}

// Load reads a .env file if present, then falls back to real env vars.
func Load() Config {
	_ = godotenv.Load() // ignore error: fine if .env doesn't exist (e.g. in prod)

	return Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/fulfillment?sslmode=disable"),
		RabbitMQURL: getEnv("RABBITMQ_URL", ""), // empty = messaging disabled
		JWTSecret:   os.Getenv("JWT_SECRET"),
		JWTIssuer:   getEnv("JWT_ISSUER", "gelatoflow-auth"),
		JWTAudience: getEnv("JWT_AUDIENCE", "gelatoflow-api"),
	}
}

// Validate rejects settings the service must not start with.
func (c Config) Validate() error {
	if len([]byte(c.JWTSecret)) < MinimumJWTSecretBytes {
		return fmt.Errorf("JWT_SECRET must contain at least %d bytes", MinimumJWTSecretBytes)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
