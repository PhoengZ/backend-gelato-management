package tests

import (
	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/transport"
	"batch-inventory-service/migrations"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func productionInput() inventory.CreateInput {
	cost := int64(125)
	return inventory.CreateInput{FlavorID: uuid.NewString(), ProductionDate: time.Now().UTC().Format("2006-01-02"), ExpiresAt: time.Now().Add(time.Hour), Initial: 4, UnitCost: &cost, Currency: "THB"}
}

func TestProducedMovementUsesVerifiedManagerActor(t *testing.T) {
	s, _ := fixture(t)
	v := auth.NewVerifier(jwtSecret, "gelatoflow-auth", "gelatoflow-api")
	h := transport.NewHTTP(s, v, time.Second)
	manager := token(t, auth.RoleManager)
	claims, err := v.Parse(manager)
	if err != nil {
		t.Fatal(err)
	}
	in := productionInput()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/inventory/batches", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+manager)
	req.Header.Set("X-User-ID", uuid.NewString())
	req.Header.Set("X-User-Role", "CUSTOMER")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var created inventory.Batch
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var actor, role string
	if err := s.DB.QueryRow(context.Background(), "SELECT actor_id::text,actor_role FROM inventory_movements WHERE batch_id=$1 AND kind='PRODUCED'", created.ID).Scan(&actor, &role); err != nil || actor != claims.Subject || role != "MANAGER" {
		t.Fatal("wrong creation attribution", actor, role, err)
	}
	if _, err := s.Create(context.Background(), productionInput()); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal("unattributed direct create allowed", err)
	}
	if _, err := s.Create(inventory.WithActor(context.Background(), uuid.NewString(), "STAFF"), productionInput()); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal("staff direct create allowed", err)
	}
	spoof := strings.TrimSuffix(string(raw), "}") + fmt.Sprintf(",\"actor_id\":%q}", uuid.NewString())
	call(t, h, "POST", "/api/v1/inventory/batches", spoof, manager, 400)
	var count int
	if err := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM batches").Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected create changed stock", count, err)
	}
	staff := token(t, auth.RoleStaff)
	staffClaims, _ := v.Parse(staff)
	waste := fmt.Sprintf("{\"portions\":1,\"reason\":\"DAMAGED\",\"idempotency_key\":%q}", uuid.NewString())
	call(t, h, "POST", "/api/v1/inventory/batches/"+created.ID+"/waste", waste, staff, 201)
	if err := s.DB.QueryRow(context.Background(), "SELECT actor_id::text,actor_role FROM inventory_movements WHERE batch_id=$1 AND kind='WASTED'", created.ID).Scan(&actor, &role); err != nil || actor != staffClaims.Subject || role != "STAFF" {
		t.Fatal("wrong waste attribution", actor, role, err)
	}
}

func TestProductionAuditFailureRollsBackTheBatch(t *testing.T) {
	s, _ := fixture(t)
	if _, err := s.DB.Exec(context.Background(), "CREATE FUNCTION reject_movement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure'; END $$; CREATE TRIGGER reject_movement BEFORE INSERT ON inventory_movements FOR EACH ROW EXECUTE FUNCTION reject_movement()"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(inventory.WithActor(context.Background(), uuid.NewString(), "MANAGER"), productionInput()); err == nil {
		t.Fatal("creation succeeded without audit")
	}
	var count int
	if err := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM batches").Scan(&count); err != nil || count != 0 {
		t.Fatal("batch committed without history", count, err)
	}
}

func TestMovementActorMigrationPreservesUnknownHistory(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	old := batch(t, s, uuid.NewString(), 4, time.Now().Add(time.Hour))
	if _, err := s.DB.Exec(ctx, "ALTER TABLE inventory_movements DROP COLUMN actor_id, DROP COLUMN actor_role; DELETE FROM inventory_schema_migrations WHERE version='005_movement_actor.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.CheckReady(ctx, s.DB); err == nil {
		t.Fatal("old actor schema advertised readiness")
	}
	for range 2 {
		if err := migrations.Apply(ctx, s.DB); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.CheckReady(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	var unknown bool
	if err := s.DB.QueryRow(ctx, "SELECT actor_id IS NULL AND actor_role IS NULL FROM inventory_movements WHERE batch_id=$1", old.ID).Scan(&unknown); err != nil || !unknown {
		t.Fatal("invented historical actor", unknown, err)
	}
	assertBalance(t, s, old.ID, 4, 0, 0, 0)
	if _, err := s.DB.Exec(ctx, "ALTER TABLE inventory_movements DROP COLUMN actor_id"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.CheckReady(ctx, s.DB); err == nil {
		t.Fatal("actor column drift ignored despite marker")
	}
}
