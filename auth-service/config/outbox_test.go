package config

import (
	"crypto/tls"
	"strings"
	"testing"
)

func TestOutboxDisabledLeavesLegacyRuntimeIndependentOfBroker(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("OUTBOX_AMQP_URL", "malformed private-broker-secret")
	t.Setenv("OUTBOX_EXCHANGE", "")
	cfg, err := Load()
	if err != nil || cfg.Outbox.Enabled || cfg.Outbox.TLSConfig != nil {
		t.Fatal("default runtime unexpectedly requires a broker", err)
	}
}

func TestOutboxConfigurationAndBoundedErrors(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"OUTBOX_PUBLISH_ENABLED", "perhaps"},
		{"OUTBOX_AMQP_URL", "https://private-person:private-secret@private-host/"},
		{"OUTBOX_AMQP_URL", ""},
		{"OUTBOX_AMQP_URL", "amqp://private-person:private-secret@host:invalid/private-vhost"},
		{"OUTBOX_EXCHANGE", ""}, {"OUTBOX_EXCHANGE", "amq.topic"}, {"OUTBOX_EXCHANGE", "bad\nname"},
		{"OUTBOX_POLL_INTERVAL", "0s"}, {"OUTBOX_POLL_INTERVAL", "61s"},
		{"OUTBOX_PUBLISH_TIMEOUT", "99ms"}, {"OUTBOX_PUBLISH_TIMEOUT", "31s"},
		{"OUTBOX_TLS_CA_FILE", "private-file-path"},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			setValidEnvironment(t)
			t.Setenv("OUTBOX_PUBLISH_ENABLED", "true")
			t.Setenv("OUTBOX_AMQP_URL", "amqp://private-person:private-secret@private-host/private-vhost")
			t.Setenv("OUTBOX_EXCHANGE", "auth")
			t.Setenv("OUTBOX_POLL_INTERVAL", "1s")
			t.Setenv("OUTBOX_PUBLISH_TIMEOUT", "5s")
			t.Setenv("OUTBOX_TLS_CA_FILE", "")
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil {
				t.Fatal("unsafe configuration accepted")
			}
			for _, private := range []string{"private-person", "private-secret", "private-host", "private-vhost", "private-file-path"} {
				if strings.Contains(err.Error(), private) {
					t.Fatal("configuration diagnostic exposed private input")
				}
			}
		})
	}
}

func TestOutboxAMQPSRequiresVerifiedTLS(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("OUTBOX_PUBLISH_ENABLED", "true")
	t.Setenv("OUTBOX_AMQP_URL", "amqps://example.invalid/")
	t.Setenv("OUTBOX_EXCHANGE", "auth")
	t.Setenv("OUTBOX_TLS_CA_FILE", "")
	cfg, err := Load()
	if err != nil || cfg.Outbox.TLSConfig == nil || cfg.Outbox.TLSConfig.InsecureSkipVerify || cfg.Outbox.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("verified TLS configuration missing", err)
	}
}
