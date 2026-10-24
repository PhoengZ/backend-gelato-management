package migrations

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBatchCodeMigrationPreservesExistingStock(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		if os.Getenv("INTEGRATION_REQUIRED") == "1" {
			t.Fatal("TEST_DATABASE_URL required")
		}
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "inventory_migration_test_" + fmt.Sprintf("%x", uuid.New())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy, err := files.ReadFile("001_inventory.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, string(legacy)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, "CREATE TABLE inventory_schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT clock_timestamp()); INSERT INTO inventory_schema_migrations(version) VALUES('001_inventory.sql')"); err != nil {
		t.Fatal(err)
	}
	id, flavor := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err = db.Exec(ctx, `INSERT INTO batches(id,flavor_id,production_date,expires_at,initial_portions,available_portions,reserved_portions,sold_portions,wasted_portions,unit_cost_minor,currency,status,created_at,updated_at)
	 VALUES($1,$2,'2026-10-01','2026-10-12T12:00:00Z',6,2,0,3,1,125,'THB','ACTIVE',$3,$3)`, id, flavor, created); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = Apply(ctx, db); err != nil {
			t.Fatal("upgrade/reapply failed", err)
		}
	}
	var actualID, actualFlavor, code string
	var initial, available, reserved, sold, wasted int64
	var updated time.Time
	if err = db.QueryRow(ctx, "SELECT id::text,flavor_id::text,batch_code,initial_portions,available_portions,reserved_portions,sold_portions,wasted_portions,updated_at FROM batches").Scan(&actualID, &actualFlavor, &code, &initial, &available, &reserved, &sold, &wasted, &updated); err != nil {
		t.Fatal(err)
	}
	if actualID != id || actualFlavor != flavor || code != "BATCH-"+strings.ToUpper(id) || initial != 6 || available != 2 || reserved != 0 || sold != 3 || wasted != 1 || !updated.Equal(created) {
		t.Fatal("upgrade changed existing identifiers/stock/timestamp", actualID, actualFlavor, code, initial, available, reserved, sold, wasted, updated)
	}
	var versions int
	if err = db.QueryRow(ctx, "SELECT count(*) FROM inventory_schema_migrations").Scan(&versions); err != nil || versions != 4 {
		t.Fatal("upgrade not recorded exactly once", err, versions)
	}
	// A second row cannot adopt the backfilled label, proving database uniqueness
	// rather than relying only on a prior application-level lookup.
	_, err = db.Exec(ctx, `INSERT INTO batches(id,batch_code,flavor_id,production_date,expires_at,initial_portions,available_portions,unit_cost_minor,currency,status,created_at,updated_at)
	 VALUES($1,$2,$3,'2026-10-01','2026-10-12T12:00:00Z',1,1,125,'THB','ACTIVE',$4,$4)`, uuid.NewString(), code, flavor, created)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" || pe.ConstraintName != "batches_batch_code_key" {
		t.Fatal("database accepted duplicate upgraded label", err)
	}
}
