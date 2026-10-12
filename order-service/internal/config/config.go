package config

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                  string
	DatabaseURL           string
	RabbitMQURL           string
	BatchInventoryGRPCURL string
	OrderServiceToken     string
	CatalogGRPCURL        string
	PaymentServiceToken   string
	PaymentGRPCURL        string
	PaymentRequestToken   string
	PaymentRequestTimeout time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load()
	cfg := Config{
		Port:                  value("PORT", "8080"),
		DatabaseURL:           value("DATABASE_URL", ""),
		RabbitMQURL:           value("RABBITMQ_URL", ""),
		BatchInventoryGRPCURL: value("BATCH_INVENTORY_GRPC_URL", ""),
		OrderServiceToken:     value("ORDER_SERVICE_TOKEN", ""),
		CatalogGRPCURL:        value("CATALOG_GRPC_URL", ""),
		PaymentServiceToken:   value("PAYMENT_SERVICE_TOKEN", ""),
		PaymentGRPCURL:        value("PAYMENT_GRPC_URL", ""),
		PaymentRequestToken:   value("PAYMENT_REQUEST_TOKEN", ""),
	}
	if cfg.Port == "" || strings.ContainsAny(cfg.Port, ":/\\") {
		return Config{}, errors.New("PORT must be a port number")
	}
	if cfg.DatabaseURL == "" || cfg.RabbitMQURL == "" || cfg.BatchInventoryGRPCURL == "" || cfg.OrderServiceToken == "" || cfg.CatalogGRPCURL == "" || cfg.PaymentServiceToken == "" {
		return Config{}, errors.New("DATABASE_URL, RABBITMQ_URL, BATCH_INVENTORY_GRPC_URL, ORDER_SERVICE_TOKEN, CATALOG_GRPC_URL, and PAYMENT_SERVICE_TOKEN are required")
	}
	if len([]byte(cfg.OrderServiceToken)) < 32 || len([]byte(cfg.PaymentServiceToken)) < 32 {
		return Config{}, errors.New("ORDER_SERVICE_TOKEN and PAYMENT_SERVICE_TOKEN must each contain at least 32 bytes")
	}
	_, paymentPort, addrErr := net.SplitHostPort(cfg.PaymentGRPCURL)
	port, portErr := strconv.Atoi(paymentPort)
	if addrErr != nil || portErr != nil || port < 1 || port > 65535 || strings.ContainsAny(cfg.PaymentGRPCURL, " \t\r\n") {
		return Config{}, errors.New("PAYMENT_GRPC_URL must be host:port with a valid port")
	}
	if len(cfg.PaymentRequestToken) < 32 || strings.ContainsAny(cfg.PaymentRequestToken, " \t\r\n") || cfg.PaymentRequestToken == cfg.OrderServiceToken || cfg.PaymentRequestToken == cfg.PaymentServiceToken {
		return Config{}, errors.New("PAYMENT_REQUEST_TOKEN must be a distinct service token of at least 32 bytes without whitespace")
	}
	var timeoutErr error
	cfg.PaymentRequestTimeout, timeoutErr = time.ParseDuration(value("PAYMENT_REQUEST_TIMEOUT", "5s"))
	if timeoutErr != nil || cfg.PaymentRequestTimeout <= 0 || cfg.PaymentRequestTimeout > 30*time.Second {
		return Config{}, errors.New("PAYMENT_REQUEST_TIMEOUT must be positive and at most 30s")
	}
	return cfg, nil
}

func value(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
