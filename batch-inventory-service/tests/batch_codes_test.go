package tests

import (
	"batch-inventory-service/internal/app"
	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/config"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/transport"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBatchCodesHTTPPersistenceAndConflicts(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	h := transport.NewHTTP(s, auth.NewVerifier(jwtSecret, "gelatoflow-auth", "gelatoflow-api"), time.Second)
	manager := token(t, auth.RoleManager)
	flavor := uuid.NewString()
	input := map[string]any{
		"batch_code": " gf-20261010-choc_01 ", "flavor_id": flavor,
		"production_date":  time.Now().UTC().Format("2006-01-02"),
		"expires_at":       time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"initial_portions": 6, "unit_cost_minor": 125, "currency": "THB",
	}
	create := func(expected int) []byte {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return call(t, h, http.MethodPost, "/api/v1/inventory/batches", string(body), manager, expected)
	}
	var created inventory.Batch
	if err := json.Unmarshal(create(201), &created); err != nil {
		t.Fatal(err)
	}
	if created.BatchCode != "GF-20261010-CHOC_01" || created.ID == "" {
		t.Fatal("code was not normalized alongside UUID", created)
	}
	var persisted inventory.Batch
	if err := json.Unmarshal(call(t, h, http.MethodGet, "/api/v1/inventory/batches/"+created.ID, "", manager, 200), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.BatchCode != created.BatchCode || persisted.ID != created.ID {
		t.Fatal("read did not preserve production label", persisted)
	}
	var listed struct {
		Items []inventory.Batch `json:"items"`
	}
	if err := json.Unmarshal(call(t, h, http.MethodGet, "/api/v1/inventory/batches", "", manager, 200), &listed); err != nil || len(listed.Items) != 1 || listed.Items[0].BatchCode != created.BatchCode {
		t.Fatal("list did not expose persisted label", err, listed)
	}
	var code string
	if err := s.DB.QueryRow(ctx, "SELECT batch_code FROM batches WHERE id=$1", created.ID).Scan(&code); err != nil || code != created.BatchCode {
		t.Fatal("label not stored in PostgreSQL", err, code)
	}

	// A duplicate normalized label is a conflict even for a different flavor.
	input["batch_code"] = "GF-20261010-choc_01"
	input["flavor_id"] = uuid.NewString()
	var conflict struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(create(409), &conflict); err != nil || conflict.Code != "BATCH_CODE_CONFLICT" {
		t.Fatal("duplicate code classification", err, conflict)
	}
	for _, bad := range []any{"", "   ", "_CODE", "-CODE", "CHOC/01", "CODE 01", "ชา", "ſ1", strings.Repeat("A", 65), nil} {
		input["batch_code"] = bad
		create(400)
	}
	var batches, movements int
	if err := s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM batches),(SELECT count(*) FROM inventory_movements)").Scan(&batches, &movements); err != nil || batches != 1 || movements != 1 {
		t.Fatal("invalid/duplicate code created stock side effects", err, batches, movements)
	}
	assertBalance(t, s, created.ID, 6, 0, 0, 0)

	// Existing callers may omit the field; generated labels remain stable and unique.
	delete(input, "batch_code")
	var first, second inventory.Batch
	if err := json.Unmarshal(create(201), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(create(201), &second); err != nil {
		t.Fatal(err)
	}
	if first.BatchCode != "BATCH-"+strings.ToUpper(first.ID) || second.BatchCode == first.BatchCode {
		t.Fatal("legacy create did not get distinct labels", first, second)
	}
	got, err := s.Get(ctx, first.ID)
	if err != nil || got.BatchCode != first.BatchCode {
		t.Fatal("generated label not persisted", err, got)
	}
	for _, valid := range []string{"1", strings.Repeat("A", 64)} {
		input["batch_code"] = valid
		create(201)
	}
}

func TestConcurrentBatchCodeUniqueness(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	cost := int64(125)
	code := "SHARED-PRODUCTION-CODE"
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		// Different flavor locks exercise the global database uniqueness constraint.
		in := inventory.CreateInput{BatchCode: &code, FlavorID: uuid.NewString(), ProductionDate: time.Now().UTC().Format("2006-01-02"), ExpiresAt: time.Now().Add(time.Hour), Initial: 4, UnitCost: &cost, Currency: "THB"}
		go func() {
			<-start
			_, err := s.Create(inventory.WithActor(ctx, uuid.NewString(), "MANAGER"), in)
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, inventory.ErrBatchCodeConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("concurrent duplicate label accepted", successes, conflicts)
	}
	var batches, movements int
	var available int64
	if err := s.DB.QueryRow(ctx, "SELECT count(*),sum(available_portions),(SELECT count(*) FROM inventory_movements) FROM batches").Scan(&batches, &available, &movements); err != nil || batches != 1 || available != 4 || movements != 1 {
		t.Fatal("conflict partially produced stock", err, batches, available, movements)
	}
}

func TestStartupRequiresBatchCodeMigration(t *testing.T) {
	s, raw := fixture(t)
	if _, err := s.DB.Exec(context.Background(), "DELETE FROM inventory_schema_migrations WHERE version='002_batch_code.sql'"); err != nil {
		t.Fatal(err)
	}
	err := app.Run(context.Background(), config.Config{DatabaseURL: raw, CatalogAddr: "127.0.0.1:1", HTTPAddr: "invalid address", GRPCAddr: "invalid address", RequestTimeout: time.Second})
	if err == nil || err.Error() != "run Inventory migrations before starting service" {
		t.Fatal("startup accepted missing batch-code migration", err)
	}
}
