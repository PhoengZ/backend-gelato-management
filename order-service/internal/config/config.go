package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                   string
	DatabaseURL            string
	RabbitMQURL            string
	BatchInventoryGRPC_URL string
}

func LoadConfig() Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, relying on environment variables")
	}

	return Config{
		Port:                   getEnv("PORT", "8080"),
		DatabaseURL:            getEnv("DATABASE_URL", "host=localhost user=postgres password=postgres dbname=order_db port=5432 sslmode=disable"),
		RabbitMQURL:            getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		BatchInventoryGRPC_URL: getEnv("BATCH_INVENTORY_GRPC_URL", "localhost:50051"),
	}
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
