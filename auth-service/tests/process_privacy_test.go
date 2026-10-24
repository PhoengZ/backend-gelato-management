package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorageDiagnosticsDoNotExposeDSNValues(t *testing.T) {
	for _, entry := range []string{"api", "migrate"} {
		t.Run(entry, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), entry)
			if output, err := exec.Command("go", "build", "-race", "-o", binary, "../cmd/"+entry).CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			for _, dsn := range []string{"postgres://private-person%40example.invalid:private-secret@127.0.0.1:notaport/private-database", "postgres://private-person:private-secret@127.0.0.1:1/private-database?sslmode=disable"} {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				command := exec.CommandContext(ctx, binary)
				command.Env = append(os.Environ(), "DATABASE_URL="+dsn, "JWT_SECRET=local_test_secret_with_at_least_32_bytes", "HTTP_TLS_CERT_FILE=", "HTTP_TLS_KEY_FILE=")
				output, err := command.CombinedOutput()
				cancel()
				if err == nil || ctx.Err() == context.DeadlineExceeded {
					t.Fatal("invalid/unavailable database did not fail promptly", err)
				}
				for _, secret := range []string{"private-person", "example.invalid", "private-secret", "private-database"} {
					if strings.Contains(string(output), secret) {
						t.Fatalf("storage diagnostics exposed %q: %s", secret, output)
					}
				}
				if !strings.Contains(string(output), "PostgreSQL") || !strings.Contains(string(output), "error_type=") {
					t.Fatal("bounded diagnostics missing", string(output))
				}
			}
		})
	}
}
