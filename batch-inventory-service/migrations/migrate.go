// Package migrations embeds versioned Inventory-only schema migrations.
package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

var ErrNotReady = errors.New("run Inventory migrations before starting service")

// CheckReady requires every forward migration embedded in this binary, while
// accepting additional markers written by newer binaries. Probe the actual
// columns as well so a stale/incorrect marker cannot advertise readiness.
func CheckReady(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := files.ReadDir(".")
	if err != nil {
		return ErrNotReady
	}
	var required []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") && !strings.HasSuffix(entry.Name(), ".down.sql") {
			required = append(required, entry.Name())
		}
	}
	var complete bool
	if err := pool.QueryRow(ctx, "SELECT count(*)=$2 FROM inventory_schema_migrations WHERE version=ANY($1::text[])", required, len(required)).Scan(&complete); err != nil || !complete {
		return ErrNotReady
	}
	rows, err := pool.Query(ctx, "SELECT batch_code,low_stock_notified_at,expiring_notified_at FROM batches LIMIT 0")
	if err != nil {
		return ErrNotReady
	}
	rows.Close()
	if rows.Err() != nil {
		return ErrNotReady
	}
	return nil
}

func parseVersion(name string) int {
	var v int
	for _, ch := range name {
		if ch >= '0' && ch <= '9' {
			v = v*10 + int(ch-'0')
		} else {
			break
		}
	}
	return v
}

func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(482119711)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS inventory_schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT clock_timestamp())"); err != nil {
		return err
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		vi, vj := parseVersion(entries[i].Name()), parseVersion(entries[j].Name())
		if vi != vj {
			return vi < vj
		}
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".down.sql") {
			continue
		}
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM inventory_schema_migrations WHERE version=$1)", entry.Name()).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO inventory_schema_migrations(version) VALUES($1)", entry.Name()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
