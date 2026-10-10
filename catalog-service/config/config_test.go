package config

import "testing"

func TestLoadAcceptsValidConfiguration(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("GRPC_ADDR", "")
	t.Setenv("REDIS_URL", "redis://:password@localhost:6379/0")
	t.Setenv("JWT_SECRET", "test_secret_that_is_longer_than_32_bytes")
	t.Setenv("REDIS_KEY_PREFIX", "catalog:test")
	t.Setenv("REQUEST_TIMEOUT", "2s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Port != "3002" || cfg.GRPCAddr != "127.0.0.1:50052" || cfg.RedisKeyPrefix != "catalog:test" || cfg.RequestTimeout.String() != "2s" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestGRPCAddressConfiguration(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("JWT_SECRET", "test_secret_that_is_longer_than_32_bytes")
	for _, addr := range []string{"0.0.0.0:50052", "[::1]:50052", "catalog-service:50052"} {
		t.Setenv("GRPC_ADDR", addr)
		cfg, err := Load()
		if err != nil || cfg.GRPCAddr != addr {
			t.Fatalf("valid address rejected: %s %v", addr, err)
		}
	}
	for _, addr := range []string{"50052", "localhost:0", "localhost:65536", "localhost:-1", "localhost:http", "bad host:50052"} {
		t.Setenv("GRPC_ADDR", addr)
		if _, err := Load(); err == nil {
			t.Fatalf("invalid address accepted: %s", addr)
		}
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
