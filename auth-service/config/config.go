package config

import (
	"auth-service/internal/transportsecurity"
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const minimumJWTSecretBytes = 32

type Config struct {
	HTTPTLS        *tls.Config
	Port           string
	DatabaseURL    string
	JWTSecret      string
	JWTIssuer      string
	JWTAudience    string
	JWTTTL         time.Duration
	BcryptCost     int
	RequestTimeout time.Duration
	Outbox         OutboxConfig
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		Port:        envOrDefault("PORT", "3001"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
		JWTIssuer:   configuredClaim("JWT_ISSUER", "gelatoflow-auth"),
		JWTAudience: configuredClaim("JWT_AUDIENCE", "gelatoflow-api"),
	}

	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len([]byte(cfg.JWTSecret)) < minimumJWTSecretBytes || strings.TrimSpace(cfg.JWTSecret) == "" {
		return Config{}, fmt.Errorf("JWT_SECRET must contain at least %d bytes", minimumJWTSecretBytes)
	}
	if strings.TrimSpace(cfg.JWTIssuer) == "" || strings.TrimSpace(cfg.JWTAudience) == "" {
		return Config{}, fmt.Errorf("JWT_ISSUER and JWT_AUDIENCE cannot be blank")
	}
	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("PORT must be between 1 and 65535")
	}

	ttl, err := time.ParseDuration(envOrDefault("JWT_TTL", "15m"))
	if err != nil || ttl < time.Second || ttl > time.Hour || ttl%time.Second != 0 {
		return Config{}, fmt.Errorf("JWT_TTL must be whole seconds between 1s and 1h")
	}
	cfg.JWTTTL = ttl

	cost, err := strconv.Atoi(envOrDefault("BCRYPT_COST", "12"))
	if err != nil || cost < 10 || cost > 14 {
		return Config{}, fmt.Errorf("BCRYPT_COST must be between 10 and 14")
	}
	cfg.BcryptCost = cost
	timeout, err := time.ParseDuration(envOrDefault("REQUEST_TIMEOUT", "5s"))
	if err != nil || timeout <= 0 || timeout > 30*time.Second {
		return Config{}, fmt.Errorf("REQUEST_TIMEOUT must be positive and at most 30s")
	}
	cfg.RequestTimeout = timeout
	if cfg.HTTPTLS, err = transportsecurity.ServerFromEnv("HTTP_TLS"); err != nil {
		return Config{}, err
	}
	if cfg.Outbox, err = loadOutbox(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func configuredClaim(name, fallback string) string {
	if value, set := os.LookupEnv(name); set {
		return value
	}
	return fallback
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
