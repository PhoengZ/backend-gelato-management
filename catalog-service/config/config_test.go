package config

import "testing"

func TestLoadAcceptsValidConfiguration(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("REDIS_URL", "redis://:password@localhost:6379/0")
	t.Setenv("JWT_SECRET", "test_secret_that_is_longer_than_32_bytes")
	t.Setenv("REDIS_KEY_PREFIX", "catalog:test")
	t.Setenv("REQUEST_TIMEOUT", "2s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Port != "3002" || cfg.RedisKeyPrefix != "catalog:test" || cfg.RequestTimeout.String() != "2s" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsUnsafeConfiguration(t *testing.T) {
	t.Setenv("REDIS_URL", "http://localhost:6379")
	t.Setenv("JWT_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid Redis URL to fail")
	}

	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	if _, err := Load(); err == nil {
		t.Fatal("expected short JWT secret to fail")
	}

	t.Setenv("JWT_SECRET", "test_secret_that_is_longer_than_32_bytes")
	t.Setenv("REDIS_KEY_PREFIX", "contains whitespace")
	if _, err := Load(); err == nil {
		t.Fatal("expected unsafe key prefix to fail")
	}
}
