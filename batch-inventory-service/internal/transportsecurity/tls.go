// Package transportsecurity configures opt-in TLS without changing local callers.
package transportsecurity

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
)

// ServerFromEnv returns nil only when both files are absent. Bad or partial TLS
// configuration is an error; it must never silently start a plaintext listener.
func ServerFromEnv(prefix string) (*tls.Config, error) {
	cert, key := os.Getenv(prefix+"_CERT_FILE"), os.Getenv(prefix+"_KEY_FILE")
	if cert == "" && key == "" {
		return nil, nil
	}
	if cert == "" || key == "" {
		return nil, fmt.Errorf("%s requires both certificate and key files", prefix)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("%s could not load a valid certificate/key pair", prefix)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}

// HTTP wraps only HTTP listeners. gRPC uses credentials.NewTLS separately.
// Fiber implements HTTP/1.1; do not advertise HTTP/2 on its TLS connection.
func HTTP(listener net.Listener, config *tls.Config) net.Listener {
	if config == nil {
		return listener
	}
	clone := config.Clone()
	clone.NextProtos = []string{"http/1.1"}
	return tls.NewListener(listener, clone)
}
