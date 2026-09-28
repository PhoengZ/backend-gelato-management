package config

import "testing"

func baseline(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("JWT_SECRET", "jwt_test_secret_longer_than_32_bytes")
	t.Setenv("ORDER_SERVICE_TOKEN", "order_test_token_longer_than_32_bytes")
	for _, k := range []string{"HTTP_ADDR", "GRPC_ADDR", "CATALOG_GRPC_ADDR", "REQUEST_TIMEOUT", "RESERVATION_TTL", "SWEEP_INTERVAL", "PUBLISH_EVENTS", "RABBITMQ_URL"} {
		t.Setenv(k, "")
	}
}
func TestDefaultsAndInvalidConfiguration(t *testing.T) {
	baseline(t)
	c, err := Load()
	if err != nil || c.HTTPAddr != ":3004" || c.GRPCAddr != "127.0.0.1:50051" || c.PublishEvents {
		t.Fatal(c, err)
	}
	for _, tc := range []struct{ k, v string }{{"DATABASE_URL", "http://localhost"}, {"JWT_SECRET", "short"}, {"ORDER_SERVICE_TOKEN", "short"}, {"ORDER_SERVICE_TOKEN", "jwt_test_secret_longer_than_32_bytes"}, {"GRPC_ADDR", ":0"}, {"HTTP_ADDR", "invalid"}, {"CATALOG_GRPC_ADDR", "localhost:http"}, {"REQUEST_TIMEOUT", "-1s"}, {"RESERVATION_TTL", "0s"}, {"SWEEP_INTERVAL", "oops"}, {"PUBLISH_EVENTS", "notbool"}, {"PUBLISH_EVENTS", "true"}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			baseline(t)
			t.Setenv(tc.k, tc.v)
			if _, err := Load(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
