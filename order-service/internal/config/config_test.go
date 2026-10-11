package config

import (
	"testing"
	"time"
)

func setCheckoutEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"PORT": "3003", "DATABASE_URL": "postgres-test", "RABBITMQ_URL": "amqp-test",
		"BATCH_INVENTORY_GRPC_URL": "localhost:50051", "CATALOG_GRPC_URL": "localhost:50052",
		"ORDER_SERVICE_TOKEN":   "inventory-service-token-at-least-32-bytes",
		"PAYMENT_SERVICE_TOKEN": "payment-callback-token-at-least-32-bytes",
		"PAYMENT_GRPC_URL":      "127.0.0.1:50057", "PAYMENT_REQUEST_TOKEN": "order-to-payment-token-at-least-32-bytes",
		"PAYMENT_REQUEST_TIMEOUT": "5s",
	} {
		t.Setenv(k, v)
	}
}

func TestPaymentConfiguration(t *testing.T) {
	setCheckoutEnv(t)
	cfg, err := Load()
	if err != nil || cfg.PaymentRequestTimeout != 5*time.Second {
		t.Fatalf("valid config: %v", err)
	}
}

func TestRejectsInvalidPaymentConfiguration(t *testing.T) {
	for _, input := range []struct{ key, value string }{
		{"PAYMENT_GRPC_URL", ""}, {"PAYMENT_GRPC_URL", "localhost:99999"},
		{"PAYMENT_REQUEST_TOKEN", "short"}, {"PAYMENT_REQUEST_TOKEN", "inventory-service-token-at-least-32-bytes"},
		{"PAYMENT_REQUEST_TOKEN", "payment-callback-token-at-least-32-bytes"},
		{"PAYMENT_REQUEST_TIMEOUT", "0s"}, {"PAYMENT_REQUEST_TIMEOUT", "31s"},
	} {
		t.Run(input.key+"/"+input.value, func(t *testing.T) {
			setCheckoutEnv(t)
			t.Setenv(input.key, input.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid payment config accepted")
			}
		})
	}
}
