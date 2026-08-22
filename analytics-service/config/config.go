package config

import (
	"log"
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

	goEnv := os.Getenv("GO_ENV")

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		if goEnv == "" || goEnv == "development" {
			mongoURI = "mongodb://root:password@localhost:27017"
		} else {
			log.Fatal("MONGO_URI environment variable is required in non-development environments")
		}
	}

	rabbitMQURL := os.Getenv("RABBITMQ_URL")
	if rabbitMQURL == "" {
		if goEnv == "" || goEnv == "development" {
			rabbitMQURL = "amqp://guest:guest@localhost:5672/"
		} else {
			log.Fatal("RABBITMQ_URL environment variable is required in non-development environments")
		}
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
