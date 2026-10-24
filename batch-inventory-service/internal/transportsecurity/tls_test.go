package transportsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerTLSConfigurationAndVerifiedHTTP(t *testing.T) {
	t.Setenv("TEST_TLS_CERT_FILE", "")
	t.Setenv("TEST_TLS_KEY_FILE", "")
	if cfg, err := ServerFromEnv("TEST_TLS"); cfg != nil || err != nil {
		t.Fatal("plaintext default changed", err)
	}
	privatePath := filepath.Join(t.TempDir(), "private-person@example.invalid")
	t.Setenv("TEST_TLS_CERT_FILE", privatePath)
	if _, err := ServerFromEnv("TEST_TLS"); err == nil || strings.Contains(err.Error(), "private-person") {
		t.Fatal("partial TLS config accepted or leaked", err)
	}
	t.Setenv("TEST_TLS_KEY_FILE", privatePath)
	if _, err := ServerFromEnv("TEST_TLS"); err == nil || strings.Contains(err.Error(), "private-person") {
		t.Fatal("bad TLS files accepted or leaked", err)
	}
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	pair := fixture.TLS.Certificates[0]
	fixture.Close()
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "server.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TLS_CERT_FILE", certPath)
	t.Setenv("TEST_TLS_KEY_FILE", keyPath)
	cfg, err := ServerFromEnv("TEST_TLS")
	if err != nil || cfg.MinVersion != tls.VersionTLS12 {
		t.Fatal(cfg, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), ErrorLog: nil}
	done := make(chan error, 1)
	go func() { done <- server.Serve(HTTP(listener, cfg)) }()
	t.Cleanup(func() { server.Close(); <-done })
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("bad test certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("https://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.ProtoMajor != 1 {
		t.Fatal("verified TLS HTTP failed", response.Status)
	}
	untrusted := &http.Client{Timeout: time.Second}
	if response, err := untrusted.Get("https://" + listener.Addr().String()); err == nil {
		response.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	old := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}}
	defer old.CloseIdleConnections()
	if response, err := (&http.Client{Transport: old, Timeout: time.Second}).Get("https://" + listener.Addr().String()); err == nil {
		response.Body.Close()
		t.Fatal("TLS below 1.2 accepted")
	}
	if response, err := client.Get("http://" + listener.Addr().String()); err == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("plaintext accepted by TLS listener")
		}
	}
}
