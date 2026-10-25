package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type OutboxConfig struct {
	Enabled        bool
	URL, Exchange  string
	TLSConfig      *tls.Config
	PollInterval   time.Duration
	PublishTimeout time.Duration
}

func loadOutbox() (OutboxConfig, error) {
	enabled, err := strconv.ParseBool(envOrDefault("OUTBOX_PUBLISH_ENABLED", "false"))
	if err != nil {
		return OutboxConfig{}, fmt.Errorf("OUTBOX_PUBLISH_ENABLED must be a boolean")
	}
	cfg := OutboxConfig{Enabled: enabled}
	if !enabled {
		return cfg, nil
	}
	cfg.URL, cfg.Exchange = os.Getenv("OUTBOX_AMQP_URL"), os.Getenv("OUTBOX_EXCHANGE")
	uri, err := amqp.ParseURI(cfg.URL)
	if err != nil || strings.TrimSpace(cfg.URL) == "" || len(cfg.URL) > 4096 || uri.Host == "" || (uri.Scheme != "amqp" && uri.Scheme != "amqps") {
		return OutboxConfig{}, fmt.Errorf("OUTBOX_AMQP_URL must be an AMQP or AMQPS URI")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,200}$`).MatchString(cfg.Exchange) || strings.HasPrefix(cfg.Exchange, "amq.") {
		return OutboxConfig{}, fmt.Errorf("OUTBOX_EXCHANGE must name an explicitly provisioned exchange")
	}
	cfg.PollInterval, err = time.ParseDuration(envOrDefault("OUTBOX_POLL_INTERVAL", "1s"))
	if err != nil || cfg.PollInterval < 50*time.Millisecond || cfg.PollInterval > time.Minute {
		return OutboxConfig{}, fmt.Errorf("OUTBOX_POLL_INTERVAL must be between 50ms and 1m")
	}
	cfg.PublishTimeout, err = time.ParseDuration(envOrDefault("OUTBOX_PUBLISH_TIMEOUT", "5s"))
	if err != nil || cfg.PublishTimeout < 100*time.Millisecond || cfg.PublishTimeout > 30*time.Second {
		return OutboxConfig{}, fmt.Errorf("OUTBOX_PUBLISH_TIMEOUT must be between 100ms and 30s")
	}
	caFile := os.Getenv("OUTBOX_TLS_CA_FILE")
	if caFile != "" && uri.Scheme != "amqps" {
		return OutboxConfig{}, fmt.Errorf("OUTBOX_TLS_CA_FILE requires AMQPS")
	}
	if uri.Scheme == "amqps" {
		cfg.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if caFile != "" {
			pem, readErr := os.ReadFile(caFile)
			if readErr != nil {
				return OutboxConfig{}, fmt.Errorf("OUTBOX_TLS_CA_FILE could not be read")
			}
			roots, poolErr := x509.SystemCertPool()
			if poolErr != nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM(pem) {
				return OutboxConfig{}, fmt.Errorf("OUTBOX_TLS_CA_FILE contains no certificate")
			}
			cfg.TLSConfig.RootCAs = roots
		}
	}
	return cfg, nil
}
