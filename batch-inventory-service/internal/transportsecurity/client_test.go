package transportsecurity

import (
	"crypto/tls"
	"testing"
)

func TestClientTLSRequiresExplicitVerifiedMode(t *testing.T) {
	t.Setenv("CATALOG_GRPC_TLS_ENABLED", "")
	t.Setenv("CATALOG_GRPC_TLS_CA_FILE", "")
	t.Setenv("CATALOG_GRPC_TLS_SERVER_NAME", "")
	if cfg, err := ClientFromEnv(); err != nil || cfg != nil {
		t.Fatal("plaintext default changed", err)
	}
	t.Setenv("CATALOG_GRPC_TLS_SERVER_NAME", "catalog.internal")
	if _, err := ClientFromEnv(); err == nil {
		t.Fatal("inactive TLS trust settings ignored")
	}
	t.Setenv("CATALOG_GRPC_TLS_ENABLED", "true")
	cfg, err := ClientFromEnv()
	if err != nil || cfg.InsecureSkipVerify || cfg.MinVersion != tls.VersionTLS12 || cfg.ServerName != "catalog.internal" {
		t.Fatal(cfg, err)
	}
	t.Setenv("CATALOG_GRPC_TLS_CA_FILE", "missing-private-file")
	if _, err := ClientFromEnv(); err == nil {
		t.Fatal("unreadable CA accepted")
	}
}
