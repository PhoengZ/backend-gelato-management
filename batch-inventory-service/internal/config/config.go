package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr, GRPCAddr, DatabaseURL, CatalogAddr, JWTSecret, JWTIssuer, JWTAudience, OrderToken, RabbitURL string
	RequestTimeout, ReservationTTL, SweepInterval                                                          time.Duration
	PublishEvents                                                                                          bool
}

func env(k, d string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return d
}
func duration(k, d string) (time.Duration, error) {
	v, e := time.ParseDuration(env(k, d))
	if e != nil || v <= 0 || v > 24*time.Hour {
		return 0, fmt.Errorf("%s must be a positive duration up to 24h", k)
	}
	return v, nil
}
func address(s string) bool {
	_, port, e := net.SplitHostPort(s)
	n, err := strconv.Atoi(port)
	return e == nil && err == nil && n > 0 && n <= 65535 && !strings.ContainsAny(s, " \t\n\r")
}
func Load() (Config, error) {
	c := Config{HTTPAddr: env("HTTP_ADDR", ":3004"), GRPCAddr: env("GRPC_ADDR", "127.0.0.1:50051"), DatabaseURL: os.Getenv("DATABASE_URL"), CatalogAddr: env("CATALOG_GRPC_ADDR", "127.0.0.1:50052"), JWTSecret: os.Getenv("JWT_SECRET"), JWTIssuer: env("JWT_ISSUER", "gelatoflow-auth"), JWTAudience: env("JWT_AUDIENCE", "gelatoflow-api"), OrderToken: os.Getenv("ORDER_SERVICE_TOKEN"), RabbitURL: os.Getenv("RABBITMQ_URL")}
	if !address(c.HTTPAddr) || !address(c.GRPCAddr) || !address(c.CatalogAddr) {
		return c, fmt.Errorf("listener and Catalog addresses must be host:port")
	}
	u, e := url.Parse(c.DatabaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return c, fmt.Errorf("DATABASE_URL must be a PostgreSQL URL")
	}
	if len(c.JWTSecret) < 32 || len(c.OrderToken) < 32 || c.JWTSecret == c.OrderToken || strings.ContainsAny(c.OrderToken, " \t\r\n") {
		return c, fmt.Errorf("JWT_SECRET and ORDER_SERVICE_TOKEN must be distinct values of at least 32 bytes; service token cannot contain whitespace")
	}
	if c.RequestTimeout, e = duration("REQUEST_TIMEOUT", "3s"); e != nil {
		return c, e
	}
	if c.ReservationTTL, e = duration("RESERVATION_TTL", "10m"); e != nil {
		return c, e
	}
	if c.SweepInterval, e = duration("SWEEP_INTERVAL", "1s"); e != nil {
		return c, e
	}
	c.PublishEvents, e = strconv.ParseBool(env("PUBLISH_EVENTS", "false"))
	if e != nil {
		return c, fmt.Errorf("invalid PUBLISH_EVENTS")
	}
	if c.PublishEvents {
		u, e = url.Parse(c.RabbitURL)
		if e != nil || u.Host == "" || (u.Scheme != "amqp" && u.Scheme != "amqps") {
			return c, fmt.Errorf("RABBITMQ_URL required when publishing is enabled")
		}
	}
	return c, nil
}
