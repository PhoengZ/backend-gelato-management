// Package migrations embeds versioned Inventory-only schema migrations.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
)

//go:embed *.sql
var files embed.FS

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
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
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
