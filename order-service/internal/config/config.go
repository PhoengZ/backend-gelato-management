package config

import (
	"errors"
	"os"
	"strings"

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
	return cfg, nil
}

func value(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
