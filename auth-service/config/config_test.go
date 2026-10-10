package config

import (
	"testing"
	"time"
)

func TestLoadParsesRequiredSecurityConfiguration(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("PORT", "9090")
	t.Setenv("JWT_TTL", "20m")
	t.Setenv("BCRYPT_COST", "13")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Port != "9090" || cfg.JWTTTL != 20*time.Minute || cfg.BcryptCost != 13 {
		t.Fatalf("unexpected configuration: %+v", cfg)
	}
}

func TestLoadRejectsUnsafeOrMissingConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "missing database URL", key: "DATABASE_URL", value: ""},
		{name: "short JWT secret", key: "JWT_SECRET", value: "too-short"},
		{name: "invalid JWT TTL", key: "JWT_TTL", value: "0s"},
		{name: "fractional JWT TTL", key: "JWT_TTL", value: "1500ms"},
		{name: "subsecond JWT TTL", key: "JWT_TTL", value: "1ns"},
		{name: "long JWT TTL", key: "JWT_TTL", value: "61m"},
		{name: "blank issuer", key: "JWT_ISSUER", value: " "},
		{name: "empty issuer", key: "JWT_ISSUER", value: ""},
		{name: "blank audience", key: "JWT_AUDIENCE", value: "\t"},
		{name: "empty audience", key: "JWT_AUDIENCE", value: ""},
		{name: "blank signing secret", key: "JWT_SECRET", value: "                                "},
		{name: "unbounded timeout", key: "REQUEST_TIMEOUT", value: "31s"},
		{name: "invalid timeout", key: "REQUEST_TIMEOUT", value: "0s"},
		{name: "invalid port", key: "PORT", value: "0"},
		{name: "weak bcrypt cost", key: "BCRYPT_COST", value: "9"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("PORT", "3001")
	t.Setenv("JWT_SECRET", "test_secret_that_is_longer_than_32_bytes")
	t.Setenv("JWT_ISSUER", "gelatoflow-auth")
	t.Setenv("JWT_AUDIENCE", "gelatoflow-api")
	t.Setenv("JWT_TTL", "15m")
	t.Setenv("BCRYPT_COST", "12")
	t.Setenv("REQUEST_TIMEOUT", "5s")
}
