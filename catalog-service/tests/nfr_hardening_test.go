package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"catalog-service/internal/auth"
	"catalog-service/internal/logging"
	"catalog-service/internal/model"
	"catalog-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestNFR11RESTFlavorCreateIdempotency(t *testing.T) {
	repo, client, _ := redisRepositoryForTest(t)
	catalog := service.NewCatalogService(repo)
	app := newCatalogHandlerApp(catalog)
	managerToken := handlerToken(t, auth.RoleManager)

	idemKey := uuid.NewString()
	t.Cleanup(func() { _ = client.Del(context.Background(), "idempotency:"+idemKey).Err() })
	payload1 := `{"name":"Pistachio Gelato","description":"Authentic Sicilian pistachio","price":{"amount_minor":8500,"currency":"THB"},"allergens":["TREE_NUT"],"recipe":"pistachio paste; milk"}`

	// 1. Initial POST request with Idempotency-Key
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", strings.NewReader(payload1))
	req1.Header.Set("Authorization", "Bearer "+managerToken)
	req1.Header.Set("Idempotency-Key", idemKey)
	req1.Header.Set("Content-Type", "application/json")

	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if resp1.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp1.StatusCode)
	}
	body1, _ := io.ReadAll(resp1.Body)

	var created1 model.FlavorAdmin
	if err := json.Unmarshal(body1, &created1); err != nil {
		t.Fatalf("unmarshal first response: %v", err)
	}

	// 2. Replay with identical payload and Idempotency-Key
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", strings.NewReader(payload1))
	req2.Header.Set("Authorization", "Bearer "+managerToken)
	req2.Header.Set("Idempotency-Key", idemKey)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("replay request failed: %v", err)
	}
	if resp2.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected replayed 201 Created, got %d", resp2.StatusCode)
	}
	body2, _ := io.ReadAll(resp2.Body)

	if string(body1) != string(body2) {
		t.Fatalf("replayed response body mismatch: %s vs %s", body1, body2)
	}
	if resp2.Header.Get("X-Idempotent-Replay") != "true" {
		t.Errorf("expected X-Idempotent-Replay header")
	}

	// 3. Request with same Idempotency-Key but conflicting/different payload -> 409 Conflict
	payloadConflict := `{"name":"Hazelnut Gelato","description":"Piedmont hazelnut","price":{"amount_minor":9000,"currency":"THB"},"allergens":["TREE_NUT"],"recipe":"hazelnut paste; milk"}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", strings.NewReader(payloadConflict))
	req3.Header.Set("Authorization", "Bearer "+managerToken)
	req3.Header.Set("Idempotency-Key", idemKey)
	req3.Header.Set("Content-Type", "application/json")

	resp3, err := app.Test(req3)
	if err != nil {
		t.Fatalf("conflicting request failed: %v", err)
	}
	if resp3.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", resp3.StatusCode)
	}
	body3, _ := io.ReadAll(resp3.Body)
	var errResp map[string]string
	_ = json.Unmarshal(body3, &errResp)
	if errResp["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("expected code IDEMPOTENCY_CONFLICT, got %q", errResp["code"])
	}

	// 4. Request with invalid UUID Idempotency-Key -> 400 Bad Request
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/flavors", strings.NewReader(payload1))
	req4.Header.Set("Authorization", "Bearer "+managerToken)
	req4.Header.Set("Idempotency-Key", "invalid-uuid-string")
	req4.Header.Set("Content-Type", "application/json")

	resp4, err := app.Test(req4)
	if err != nil {
		t.Fatalf("invalid UUID request failed: %v", err)
	}
	if resp4.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", resp4.StatusCode)
	}
}

func TestNFR08ActorAttributionAndAuditLogging(t *testing.T) {
	var logBuf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(previous)

	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	app := newCatalogHandlerApp(catalog)

	actorID := "76ea43cb-42fa-44c8-bb0f-168769632a7d"
	managerToken := handlerToken(t, auth.RoleManager)

	// 1. Create mutation audit log
	createBody := `{"name":"Audit Mint","description":"Fresh mint flavor","price":{"amount_minor":5500,"currency":"THB"},"allergens":[],"recipe":"mint leaves; cream"}`
	createResp := catalogRequest(t, app, http.MethodPost, "/api/v1/flavors", createBody, managerToken, fiber.StatusCreated)

	var created model.FlavorAdmin
	if err := json.Unmarshal(createResp, &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "[AUDIT]") || !strings.Contains(logs, "actor_id="+actorID) || !strings.Contains(logs, "action=CREATE") {
		t.Errorf("expected CREATE audit log with actor %s, got: %s", actorID, logs)
	}

	// 2. Update (PATCH) mutation audit log
	logBuf.Reset()
	updateBody := `{"description":"Updated fresh mint flavor"}`
	catalogRequest(t, app, http.MethodPatch, "/api/v1/flavors/"+created.ID.String(), updateBody, managerToken, fiber.StatusOK)

	logs = logBuf.String()
	if !strings.Contains(logs, "[AUDIT]") || !strings.Contains(logs, "actor_id="+actorID) || !strings.Contains(logs, "action=UPDATE") {
		t.Errorf("expected UPDATE audit log with actor %s, got: %s", actorID, logs)
	}

	// 3. Replace (PUT) mutation audit log
	logBuf.Reset()
	replaceBody := `{"name":"Audit Mint Deluxe","description":"Deluxe mint","price":{"amount_minor":6000,"currency":"THB"},"allergens":[],"recipe":"mint; pure cream","active":true}`
	catalogRequest(t, app, http.MethodPut, "/api/v1/flavors/"+created.ID.String(), replaceBody, managerToken, fiber.StatusOK)

	logs = logBuf.String()
	if !strings.Contains(logs, "[AUDIT]") || !strings.Contains(logs, "actor_id="+actorID) || !strings.Contains(logs, "action=REPLACE") {
		t.Errorf("expected REPLACE audit log with actor %s, got: %s", actorID, logs)
	}

	// 4. Archive (DELETE) mutation audit log
	logBuf.Reset()
	catalogRequest(t, app, http.MethodDelete, "/api/v1/flavors/"+created.ID.String(), "", managerToken, fiber.StatusNoContent)

	logs = logBuf.String()
	if !strings.Contains(logs, "[AUDIT]") || !strings.Contains(logs, "actor_id="+actorID) || !strings.Contains(logs, "action=DELETE") {
		t.Errorf("expected DELETE audit log with actor %s, got: %s", actorID, logs)
	}
}

func TestNFR07MutationAndFailureLogsExcludeFreeformData(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	manager := handlerToken(t, auth.RoleManager)
	app := newCatalogHandlerApp(service.NewCatalogService(newMemoryFlavorRepository()))
	createBody := `{"name":"private-person@example.invalid\ninjected-log-record","description":"private-description","price":{"amount_minor":5500,"currency":"THB"},"allergens":[],"recipe":"private-recipe"}`
	catalogRequest(t, app, http.MethodPost, "/api/v1/flavors", createBody, manager, fiber.StatusCreated)
	failed := newCatalogHandlerApp(&stubCatalogService{createErr: errors.New("private-person@example.invalid private-storage-secret")})
	catalogRequest(t, failed, http.MethodPost, "/api/v1/flavors", replacementJSON, manager, fiber.StatusInternalServerError)
	for _, value := range []string{"private-", "example.invalid", "injected-log-record", manager} {
		if strings.Contains(output.String(), value) {
			t.Fatalf("freeform input/error reached log: %s", output.String())
		}
	}
	if !strings.Contains(output.String(), "[AUDIT]") || !strings.Contains(output.String(), "catalog request failed") {
		t.Fatal("operational receipts disappeared")
	}
}

func TestNFR07RedactorLoggerIntegration(t *testing.T) {
	var buf bytes.Buffer
	writer := logging.NewRedactorWriter(&buf)

	rawLog := "Handling request Authorization: Bearer secret-jwt-token-abcdef with password=plaintext_pass and machine_token=mach-tok-999"
	_, _ = writer.Write([]byte(rawLog))

	output := buf.String()
	if strings.Contains(output, "secret-jwt-token-abcdef") {
		t.Errorf("bearer token was not redacted: %s", output)
	}
	if strings.Contains(output, "plaintext_pass") {
		t.Errorf("password was not redacted: %s", output)
	}
	if strings.Contains(output, "mach-tok-999") {
		t.Errorf("machine token was not redacted: %s", output)
	}
	if !strings.Contains(output, "Authorization: Bearer [REDACTED]") {
		t.Errorf("expected Authorization: Bearer [REDACTED], got: %s", output)
	}
}
