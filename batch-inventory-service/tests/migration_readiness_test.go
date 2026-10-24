package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/transport"
	"batch-inventory-service/migrations"
	"github.com/google/uuid"
)

func TestLegacyAlertSchemaIsRejectedThenUpgradesWithoutStockLoss(t *testing.T) {
	s, dsn := fixture(t)
	ctx := context.Background()
	b := batch(t, s, uuid.NewString(), 4, time.Now().Add(time.Hour))
	if _, err := s.DB.Exec(ctx, "ALTER TABLE batches DROP COLUMN low_stock_notified_at, DROP COLUMN expiring_notified_at; DELETE FROM inventory_schema_migrations WHERE version='000004_add_low_stock_notified_flag.up.sql'"); err != nil {
		t.Fatal(err)
	}
	h := transport.NewHTTP(s, auth.NewVerifier(jwtSecret, "gelatoflow-auth", "gelatoflow-api"), time.Second)
	call(t, h, "GET", "/ready", "", "", 503)
	bin := filepath.Join(t.TempDir(), "inventory")
	if out, err := exec.Command("go", "build", "-race", "-o", bin, "../cmd/api").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(startup, bin)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn, "HTTP_ADDR="+address(t), "GRPC_ADDR="+address(t), "CATALOG_GRPC_ADDR=127.0.0.1:50052", "JWT_SECRET="+jwtSecret, "JWT_ISSUER=gelatoflow-auth", "JWT_AUDIENCE=gelatoflow-api", "ORDER_SERVICE_TOKEN="+serviceToken, "PUBLISH_EVENTS=false")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "run Inventory migrations") {
		t.Fatalf("new binary accepted legacy schema: %v %s", err, out)
	}
	for i := 0; i < 2; i++ {
		if err := migrations.Apply(ctx, s.DB); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec(ctx, "INSERT INTO inventory_schema_migrations(version) VALUES('999_future.sql')"); err != nil {
		t.Fatal(err)
	}
	call(t, h, "GET", "/ready", "", "", 200)
	assertBalance(t, s, b.ID, 4, 0, 0, 0)
	_, stop := startProcess(t, bin, dsn)
	stop()
	reserve(t, s, inventory.Item{FlavorID: b.FlavorID, Portions: 1})
	assertBalance(t, s, b.ID, 3, 1, 0, 0)
}

func TestReadinessRejectsSchemaDriftDespiteMigrationMarker(t *testing.T) {
	s, _ := fixture(t)
	if _, err := s.DB.Exec(context.Background(), "ALTER TABLE batches DROP COLUMN expiring_notified_at"); err != nil {
		t.Fatal(err)
	}
	h := transport.NewHTTP(s, auth.NewVerifier(jwtSecret, "gelatoflow-auth", "gelatoflow-api"), time.Second)
	call(t, h, "GET", "/ready", "", "", 503)
}
