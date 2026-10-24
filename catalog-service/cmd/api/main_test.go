package main

import (
	"context"
	"strings"
	"testing"
)

func TestRedisConfigurationFailureDoesNotEchoSensitiveURL(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://localhost/private-person%40example.invalid")
	t.Setenv("JWT_SECRET", "local_test_secret_with_at_least_32_bytes")
	t.Setenv("GRPC_ADDR", "127.0.0.1:50052")
	t.Setenv("REQUEST_TIMEOUT", "1s")
	err := run(context.Background())
	if err == nil {
		t.Fatal("invalid Redis database path was accepted")
	}
	if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "example.invalid") {
		t.Fatalf("configuration error echoed sensitive URL: %v", err)
	}
}
