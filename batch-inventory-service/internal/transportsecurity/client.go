package transportsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"strconv"
)

// ClientFromEnv uses normal chain and hostname verification; it has no
// skip-verification or opportunistic plaintext fallback mode.
func ClientFromEnv() (*tls.Config, error) {
	raw := os.Getenv("CATALOG_GRPC_TLS_ENABLED")
	if raw == "" {
		raw = "false"
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, errors.New("CATALOG_GRPC_TLS_ENABLED must be a boolean")
	}
	ca, name := os.Getenv("CATALOG_GRPC_TLS_CA_FILE"), os.Getenv("CATALOG_GRPC_TLS_SERVER_NAME")
	if !enabled {
		if ca != "" || name != "" {
			return nil, errors.New("Catalog TLS trust settings require CATALOG_GRPC_TLS_ENABLED=true")
		}
		return nil, nil
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name}
	if ca != "" {
		data, err := os.ReadFile(ca)
		if err != nil {
			return nil, errors.New("could not read Catalog TLS CA file")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("Catalog TLS CA file contains no certificates")
		}
		result.RootCAs = pool
	}
	return result, nil
}
