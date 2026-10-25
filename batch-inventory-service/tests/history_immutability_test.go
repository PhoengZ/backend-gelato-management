package tests

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestInventoryHistoryRejectsUpdateDeleteAndTruncate(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	b, err := s.Create(inventory.WithActor(ctx, uuid.NewString(), "MANAGER"), productionInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Waste(ctx, b.ID, uuid.NewString(), inventory.WasteInput{Portions: 1, Reason: "DAMAGED", Key: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	var before string
	query := `SELECT json_build_object('movements',(SELECT json_agg(m ORDER BY id) FROM inventory_movements m),'waste',(SELECT json_agg(w ORDER BY id) FROM waste_records w))::text`
	if err := s.DB.QueryRow(ctx, query).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"inventory_movements", "waste_records"} {
		for _, operation := range []string{"UPDATE " + table + " SET portions=portions+1", "DELETE FROM " + table, "TRUNCATE " + table} {
			t.Run(operation, func(t *testing.T) {
				_, err := s.DB.Exec(ctx, operation)
				var pg *pgconn.PgError
				if !errors.As(err, &pg) || pg.Code != "23514" {
					t.Fatal("history mutation was not rejected", err)
				}
			})
		}
	}
	var after string
	if err := s.DB.QueryRow(ctx, query).Scan(&after); err != nil || after != before {
		t.Fatal("rejected history operation changed evidence", err)
	}
	if _, err := s.Waste(ctx, b.ID, uuid.NewString(), inventory.WasteInput{Portions: 1, Reason: "ADJUSTMENT", Key: uuid.NewString()}); err != nil {
		t.Fatal("append-only guard blocked legitimate compensating waste", err)
	}
	if err := migrations.CheckReady(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryReadinessDetectsMissingOrDisabledHistoryGuard(t *testing.T) {
	for _, drift := range []string{"DROP TRIGGER inventory_movements_append_only ON inventory_movements", "ALTER TABLE waste_records DISABLE TRIGGER waste_records_no_truncate", "ALTER TABLE inventory_movements ENABLE REPLICA TRIGGER inventory_movements_append_only"} {
		t.Run(drift, func(t *testing.T) {
			s, _ := fixture(t)
			ctx := context.Background()
			if _, err := s.DB.Exec(ctx, drift); err != nil {
				t.Fatal(err)
			}
			if err := migrations.CheckReady(ctx, s.DB); !errors.Is(err, migrations.ErrNotReady) {
				t.Fatal("drifted guards advertised readiness", err)
			}
		})
	}
}

func TestHistoryMigrationPreservesLegacyRowsAndAlwaysProtectsReplicationSessions(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	for _, table := range []string{"inventory_movements", "waste_records"} {
		for _, suffix := range []string{"append_only", "no_truncate"} {
			if _, err := s.DB.Exec(ctx, fmt.Sprintf("DROP TRIGGER %s_%s ON %s", table, suffix, table)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.DB.Exec(ctx, "DROP FUNCTION prevent_inventory_history_modification(); DELETE FROM inventory_schema_migrations WHERE version='006_append_only_history.sql'"); err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(inventory.WithActor(ctx, uuid.NewString(), "MANAGER"), productionInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, "UPDATE inventory_movements SET actor_id=NULL,actor_role=NULL"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.DB.QueryRow(ctx, "SELECT row_to_json(m)::text FROM inventory_movements m WHERE batch_id=$1", b.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrations.CheckReady(ctx, s.DB); !errors.Is(err, migrations.ErrNotReady) {
		t.Fatal("old schema advertised readiness")
	}
	for i := 0; i < 2; i++ {
		if err := migrations.Apply(ctx, s.DB); err != nil {
			t.Fatal("migration upgrade/reapply failed", err)
		}
	}
	var after string
	if err := s.DB.QueryRow(ctx, "SELECT row_to_json(m)::text FROM inventory_movements m WHERE batch_id=$1", b.ID).Scan(&after); err != nil || before != after {
		t.Fatal("migration rewrote historical evidence", err)
	}
	connection, err := s.DB.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SET session_replication_role=replica"); err != nil {
		t.Fatal("fixture requires replication-role control", err)
	}
	defer connection.Exec(ctx, "RESET session_replication_role")
	if _, err := connection.Exec(ctx, "DELETE FROM inventory_movements"); err == nil {
		t.Fatal("replication role bypassed ALWAYS guard")
	}
}
