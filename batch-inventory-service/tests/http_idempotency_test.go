package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/transport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type inventoryReply struct {
	code int
	body string
}

func keyedInventory(h http.Handler, path, body, token string, keys ...string) inventoryReply {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for _, key := range keys {
		req.Header.Add("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return inventoryReply{response.Code, response.Body.String()}
}

func inventoryHTTP(s *inventory.Service) http.Handler {
	return transport.NewHTTP(s, auth.NewVerifier(jwtSecret, "gelatoflow-auth", "gelatoflow-api"), 5*time.Second)
}

func TestCreateBatchHeaderReplaysAcrossInstancesAndDependencyOutage(t *testing.T) {
	s, dsn := fixture(t)
	manager := token(t, auth.RoleManager)
	input := productionInput()
	raw, _ := json.Marshal(input)
	key := uuid.NewString()
	first := keyedInventory(inventoryHTTP(s), "/api/v1/inventory/batches", string(raw), manager, key)
	if first.code != 201 {
		t.Fatal(first)
	}
	var created inventory.Batch
	if err := json.Unmarshal([]byte(first.body), &created); err != nil {
		t.Fatal(err)
	}
	waste := fmt.Sprintf(`{"portions":1,"reason":"DAMAGED","idempotency_key":%q}`, uuid.NewString())
	call(t, inventoryHTTP(s), "POST", "/api/v1/inventory/batches/"+created.ID+"/waste", waste, token(t, auth.RoleStaff), 201)
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	// A committed retry does not depend on current Catalog availability, active
	// status or stock balances, and returns its original representation.
	restarted := inventory.New(db, nil, time.Minute)
	retry := keyedInventory(inventoryHTTP(restarted), "/api/v1/inventory/batches", string(raw), manager, strings.ToUpper(key))
	if retry.code != 201 || retry.body != first.body {
		t.Fatal("restart replay changed the result", first, retry)
	}
	input.Initial++
	changed, _ := json.Marshal(input)
	conflict := keyedInventory(inventoryHTTP(restarted), "/api/v1/inventory/batches", string(changed), manager, key)
	if conflict.code != 409 {
		t.Fatal("changed payload reused the key", conflict)
	}
	assertBalance(t, s, created.ID, 3, 0, 0, 1)
	var batches, movements int
	if err := db.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM batches),(SELECT count(*) FROM inventory_movements)").Scan(&batches, &movements); err != nil || batches != 1 || movements != 2 {
		t.Fatal("replay changed production/history", batches, movements, err)
	}
}

func TestCreateBatchHeaderSerializesConcurrentRequests(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprintf("same_%v", same), func(t *testing.T) {
			s, dsn := fixture(t)
			db, err := pgxpool.New(context.Background(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			apps := []http.Handler{inventoryHTTP(s), inventoryHTTP(inventory.New(db, activeCatalog{}, time.Minute))}
			manager, input, key := token(t, auth.RoleManager), productionInput(), uuid.NewString()
			start, results := make(chan struct{}), make(chan inventoryReply, 20)
			for i := 0; i < 20; i++ {
				go func(i int) {
					<-start
					in := input
					if !same {
						in.Initial += int64(i)
					}
					raw, _ := json.Marshal(in)
					results <- keyedInventory(apps[i%2], "/api/v1/inventory/batches", string(raw), manager, key)
				}(i)
			}
			close(start)
			successes, conflicts, original := 0, 0, ""
			for i := 0; i < 20; i++ {
				got := <-results
				if got.code == 201 {
					if successes == 0 {
						original = got.body
					} else if got.body != original {
						t.Fatal("concurrent retry changed its response", got)
					}
					successes++
				} else if got.code == 409 {
					conflicts++
				} else {
					t.Fatal(got)
				}
			}
			if (same && (successes != 20 || conflicts != 0)) || (!same && (successes != 1 || conflicts != 19)) {
				t.Fatal(successes, conflicts)
			}
			var batches, movements, keys int
			if err := db.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM batches),(SELECT count(*) FROM inventory_movements),(SELECT count(*) FROM idempotency_records)").Scan(&batches, &movements, &keys); err != nil || batches != 1 || movements != 1 || keys != 1 {
				t.Fatal("concurrent requests duplicated production", batches, movements, keys, err)
			}
		})
	}
}

func TestWasteHeaderAliasesPreserveCanonicalBodyReplay(t *testing.T) {
	s, _ := fixture(t)
	b := batch(t, s, uuid.NewString(), 10, time.Now().Add(time.Hour))
	h, staff := inventoryHTTP(s), token(t, auth.RoleStaff)
	bodyKey := uuid.NewString()
	body := fmt.Sprintf(`{"portions":1,"reason":"DAMAGED","idempotency_key":%q}`, bodyKey)
	path := "/api/v1/inventory/batches/" + b.ID + "/waste"
	legacy := keyedInventory(h, path, body, staff)
	if legacy.code != 201 {
		t.Fatal(legacy)
	}
	firstHeader := uuid.NewString()
	for _, key := range []string{firstHeader, strings.ToUpper(firstHeader), uuid.NewString()} {
		got := keyedInventory(h, path, body, staff, key)
		if got.code != 201 || got.body != legacy.body {
			t.Fatal("header alias changed body-key replay", legacy, got)
		}
	}
	changed := strings.ReplaceAll(body, bodyKey, uuid.NewString())
	if got := keyedInventory(h, path, changed, staff, firstHeader); got.code != 409 {
		t.Fatal("header key was rebound to another mutation", got)
	}
	assertBalance(t, s, b.ID, 9, 0, 0, 1)
	var waste, events, records int
	if err := s.DB.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM waste_records),(SELECT count(*) FROM outbox_events WHERE payload->>'type'='com.gelatoflow.inventory.waste-recorded.v1'),(SELECT count(*) FROM idempotency_records)").Scan(&waste, &events, &records); err != nil || waste != 1 || events != 1 || records != 3 {
		t.Fatal("alias added another stock/event mutation", waste, events, records, err)
	}
}

func TestWasteHeaderConcurrencyCommitsOnlyOnce(t *testing.T) {
	for _, differentHeaders := range []bool{false, true} {
		t.Run(fmt.Sprintf("different_headers_%v", differentHeaders), func(t *testing.T) {
			s, _ := fixture(t)
			b := batch(t, s, uuid.NewString(), 10, time.Now().Add(time.Hour))
			h, staff, key := inventoryHTTP(s), token(t, auth.RoleStaff), uuid.NewString()
			body := fmt.Sprintf(`{"portions":1,"reason":"DAMAGED","idempotency_key":%q}`, uuid.NewString())
			path := "/api/v1/inventory/batches/" + b.ID + "/waste"
			start, results := make(chan struct{}), make(chan inventoryReply, 20)
			for i := 0; i < 20; i++ {
				go func() {
					<-start
					header := key
					if differentHeaders {
						header = uuid.NewString()
					}
					results <- keyedInventory(h, path, body, staff, header)
				}()
			}
			close(start)
			original := ""
			for i := 0; i < 20; i++ {
				got := <-results
				if got.code != 201 || (original != "" && got.body != original) {
					t.Fatal(got)
				}
				original = got.body
			}
			assertBalance(t, s, b.ID, 9, 0, 0, 1)
		})
	}
}

func TestCreateBatchReplayWriteFailureRollsBackStockAndKeys(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, "CREATE FUNCTION reject_replay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test replay failure'; END $$; CREATE TRIGGER reject_replay BEFORE UPDATE ON idempotency_records FOR EACH ROW EXECUTE FUNCTION reject_replay()"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(productionInput())
	h, manager, key := inventoryHTTP(s), token(t, auth.RoleManager), uuid.NewString()
	if got := keyedInventory(h, "/api/v1/inventory/batches", string(raw), manager, key); got.code != 500 {
		t.Fatal(got)
	}
	var batches, movements, records int
	if err := s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM batches),(SELECT count(*) FROM inventory_movements),(SELECT count(*) FROM idempotency_records)").Scan(&batches, &movements, &records); err != nil || batches != 0 || movements != 0 || records != 0 {
		t.Fatal("replay failure committed partial state", batches, movements, records, err)
	}
	if _, err := s.DB.Exec(ctx, "DROP TRIGGER reject_replay ON idempotency_records"); err != nil {
		t.Fatal(err)
	}
	if got := keyedInventory(h, "/api/v1/inventory/batches", string(raw), manager, key); got.code != 201 {
		t.Fatal("retry could not recover the rolled-back key", got)
	}
}

func TestOptionalInventoryHeaderValidationPreservesAuthorization(t *testing.T) {
	s, _ := fixture(t)
	h := inventoryHTTP(s)
	raw, _ := json.Marshal(productionInput())
	manager := token(t, auth.RoleManager)
	for _, keys := range [][]string{{"invalid"}, {uuid.Nil.String()}, {uuid.NewString(), uuid.NewString()}} {
		if got := keyedInventory(h, "/api/v1/inventory/batches", string(raw), manager, keys...); got.code != 400 {
			t.Fatal("invalid header accepted", got)
		}
	}
	if got := keyedInventory(h, "/api/v1/inventory/batches", string(raw), token(t, auth.RoleStaff), "invalid"); got.code != 403 {
		t.Fatal("header parsing replaced authorization", got)
	}
	if got := keyedInventory(h, "/api/v1/inventory/batches", string(raw), manager); got.code != 201 {
		t.Fatal("existing caller now requires a header", got)
	}
}
