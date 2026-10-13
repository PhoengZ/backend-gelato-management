package migrations

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

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

// Apply executes all pending embedded SQL migrations in order.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(749201934)"); err != nil {
		return err
	}

	if _, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS fulfillment_schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
		)`); err != nil {
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
		if !strings.HasSuffix(entry.Name(), ".sql") || strings.HasSuffix(entry.Name(), ".down.sql") {
			continue
		}
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM fulfillment_schema_migrations WHERE version=$1)", entry.Name()).Scan(&exists); err != nil {
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
		if _, err = tx.Exec(ctx, "INSERT INTO fulfillment_schema_migrations(version) VALUES($1)", entry.Name()); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
