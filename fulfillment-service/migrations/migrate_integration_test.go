//go:build integration

// Run against a disposable PostgreSQL server (the test creates its own database):
//
//	FULFILLMENT_TEST_DATABASE_URL=postgres://... go test -tags integration ./migrations/...
package migrations

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// freshDatabase creates an empty database on the test server and returns a pool for it.
func freshDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("FULFILLMENT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("FULFILLMENT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)

	name := "fulfillment_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func execFile(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	sql, err := files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// TestIntegrationQueuedMigrationUpgradesExistingDatabase builds the schema as a
// production database had it before QUEUED existed (migrations 001-004 applied
// and recorded), with PREPARING rows, then runs Apply as the service does on startup.
func TestIntegrationQueuedMigrationUpgradesExistingDatabase(t *testing.T) {
	pool := freshDatabase(t)
	ctx := context.Background()

	legacy := []string{"001_create_kitchen_tickets.sql", "002_fulfillment_schema.sql",
		"003_create_fulfillment_history.sql", "004_nfr_hardening.sql"}
	if _, err := pool.Exec(ctx, `CREATE TABLE fulfillment_schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	for _, name := range legacy {
		execFile(t, pool, name)
		if _, err := pool.Exec(ctx, `INSERT INTO fulfillment_schema_migrations(version) VALUES($1)`, name); err != nil {
			t.Fatal(err)
		}
	}

	slotID, orderID := uuid.New(), uuid.New()
	var ticketID uuid.UUID
	if _, err := pool.Exec(ctx, `INSERT INTO pickup_slots (id, starts_at) VALUES ($1, '2026-10-13T14:00:00Z')`, slotID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO fulfillments (order_id, pickup_slot_id, queue_seq, queue_number, pickup_at)
		VALUES ($1, $2, 1, '01', '2026-10-13T14:05:00Z') RETURNING id`, orderID, slotID).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO fulfillment_status_history (fulfillment_id, new_status) VALUES ($1, 'PREPARING')`, ticketID); err != nil {
		t.Fatal(err)
	}

	if err := Apply(ctx, pool); err != nil {
		t.Fatalf("Apply on existing database: %v", err)
	}
	if err := Apply(ctx, pool); err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM fulfillments WHERE id = $1`, ticketID).Scan(&status); err != nil || status != "PREPARING" {
		t.Fatalf("existing ticket status = %q err=%v, want PREPARING", status, err)
	}

	// New rows default to QUEUED and the constraints accept it.
	var newStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO fulfillments (order_id, pickup_slot_id, queue_seq, queue_number, pickup_at)
		VALUES ($1, $2, 2, '02', '2026-10-13T14:06:00Z') RETURNING status`, uuid.New(), slotID).Scan(&newStatus); err != nil || newStatus != "QUEUED" {
		t.Fatalf("default status = %q err=%v, want QUEUED", newStatus, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO fulfillment_status_history (fulfillment_id, old_status, new_status)
		VALUES ($1, 'QUEUED', 'PREPARING')`, ticketID); err != nil {
		t.Fatalf("history with QUEUED: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE fulfillments SET status = 'BOGUS' WHERE id = $1`, ticketID); err == nil {
		t.Fatal("status constraint must still reject unknown values")
	}
}

// TestIntegrationMigrationsRerunAfterInitdb mirrors compose: Postgres runs every
// .sql file from docker-entrypoint-initdb.d, then the service runs Apply, which
// re-executes them because nothing is recorded yet. Every file must be re-runnable.
func TestIntegrationMigrationsRerunAfterInitdb(t *testing.T) {
	pool := freshDatabase(t)
	entries, err := files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sqlFiles int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			execFile(t, pool, e.Name())
			sqlFiles++
		}
	}
	if err := Apply(context.Background(), pool); err != nil {
		t.Fatalf("Apply after initdb: %v", err)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fulfillment_schema_migrations`).Scan(&n); err != nil || n != sqlFiles {
		t.Fatalf("recorded migrations = %d err=%v, want %d", n, err, sqlFiles)
	}
}
