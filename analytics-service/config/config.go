package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI    string
	RabbitMQURL string
	Port        string
}

func LoadConfig() Config {
	// In Docker, env vars are injected directly by docker-compose,
	// so .env file won't exist — this is expected and safe to ignore.
	_ = godotenv.Load()

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://root:password@localhost:27017"
	}

	rabbitMQURL := os.Getenv("RABBITMQ_URL")
	if rabbitMQURL == "" {
		rabbitMQURL = "amqp://guest:guest@localhost:5672/"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	return Config{
		MongoURI:    mongoURI,
		RabbitMQURL: rabbitMQURL,
		Port:        port,
	}
}
