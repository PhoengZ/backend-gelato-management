package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI             string
	RabbitMQURL          string
	Port                 string
	OrderServiceGRPCAddr string
	JWTSecret            string
	JWTIssuer            string
	JWTAudience          string
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

	orderServiceGRPCAddr := os.Getenv("ORDER_SERVICE_GRPC_ADDR")
	if orderServiceGRPCAddr == "" {
		orderServiceGRPCAddr = "localhost:50051"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "development_only_change_this_secret_32_bytes"
	}

	jwtIssuer := os.Getenv("JWT_ISSUER")
	if jwtIssuer == "" {
		jwtIssuer = "gelatoflow-auth"
	}

	jwtAudience := os.Getenv("JWT_AUDIENCE")
	if jwtAudience == "" {
		jwtAudience = "gelatoflow-api"
	}

	return Config{
		MongoURI:             mongoURI,
		RabbitMQURL:          rabbitMQURL,
		Port:                 port,
		OrderServiceGRPCAddr: orderServiceGRPCAddr,
		JWTSecret:            jwtSecret,
		JWTIssuer:            jwtIssuer,
		JWTAudience:          jwtAudience,
	}
}
