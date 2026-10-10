package config

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

const MinimumJWTSecretBytes = 32

type Config struct {
	Port                  string
	AuthServiceURL        string
	CatalogServiceURL     string
	OrderServiceURL       string
	InventoryServiceURL   string
	FulfillmentServiceURL string
	AnalyticsServiceURL   string
	PaymentServiceURL     string
	JWTSecret             string
	JWTIssuer             string
	JWTAudience           string
}

func LoadConfig() Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	return Config{
		Port:                  getEnv("PORT", "8080"),
		AuthServiceURL:        getEnv("AUTH_SERVICE_URL", "http://localhost:3001"),
		CatalogServiceURL:     getEnv("CATALOG_SERVICE_URL", "http://localhost:3002"),
		OrderServiceURL:       getEnv("ORDER_SERVICE_URL", "http://localhost:3003"),
		InventoryServiceURL:   getEnv("INVENTORY_SERVICE_URL", "http://localhost:3004"),
		FulfillmentServiceURL: getEnv("FULFILLMENT_SERVICE_URL", "http://localhost:3005"),
		AnalyticsServiceURL:   getEnv("ANALYTICS_SERVICE_URL", "http://localhost:3006"),
		PaymentServiceURL:     getEnv("PAYMENT_SERVICE_URL", "http://localhost:3007"),
		JWTSecret:             os.Getenv("JWT_SECRET"),
		JWTIssuer:             getEnv("JWT_ISSUER", "gelatoflow-auth"),
		JWTAudience:           getEnv("JWT_AUDIENCE", "gelatoflow-api"),
	}
}

func (c Config) Validate() error {
	if len([]byte(c.JWTSecret)) < MinimumJWTSecretBytes {
		return fmt.Errorf("JWT_SECRET must contain at least %d bytes", MinimumJWTSecretBytes)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

