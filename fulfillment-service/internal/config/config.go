package config

import (
	"os"

	"github.com/joho/godotenv"
)

// Config holds every environment-driven setting the service needs.
type Config struct {
	Port        string
	DatabaseURL string
	RabbitMQURL string // optional: leave empty to run without a broker
}

// Load reads a .env file if present, then falls back to real env vars.
func Load() Config {
	_ = godotenv.Load() // ignore error: fine if .env doesn't exist (e.g. in prod)

	return Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/fulfillment?sslmode=disable"),
		RabbitMQURL: getEnv("RABBITMQ_URL", ""), // empty = messaging disabled
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
