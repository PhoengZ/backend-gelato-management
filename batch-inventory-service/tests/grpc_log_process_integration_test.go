package tests

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestInventoryGRPCDiagnosticLogsExcludeUnauthenticatedMetadata(t *testing.T) {
	_, dsn := fixture(t)
	bin := filepath.Join(t.TempDir(), "inventory-service")
	build := exec.Command("go", "build", "-race", "-o", bin, "../cmd/api")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	logPath := filepath.Join(t.TempDir(), "diagnostic-process.log")
	_, stop, grpcAddr := startProcessOptions(t, bin, dsn, []string{"GRPC_GO_LOG_SEVERITY_LEVEL=info", "GRPC_GO_LOG_VERBOSITY_LEVEL=2", "GODEBUG=http2debug=2"}, logPath)
	transport := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, contentType := range []string{"application/grpc", "application/grpc+private-subtype@example.invalid"} {
		request, err := http.NewRequest("POST", "http://"+grpcAddr+"/catalog.v1.CatalogService/ListFlavors", bytes.NewReader([]byte{0, 0, 0, 0, 0}))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("TE", "trailers")
		if contentType == "application/grpc" {
			request.Header.Set("x-private-bin", "private-person@example.invalid")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	stop()
	output, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-person", "private-subtype", "example.invalid"} {
		if strings.Contains(string(output), secret) {
			t.Errorf("gRPC diagnostic mode leaked %q: %s", secret, output)
		}
	}
	if !strings.Contains(string(output), "[gRPC]") {
		t.Errorf("gRPC operational diagnostics are missing: %s", output)
	}
}
