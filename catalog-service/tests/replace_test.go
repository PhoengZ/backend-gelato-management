package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"catalog-service/internal/auth"
	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const replacementJSON = `{"name":"Strawberry","description":"Fresh berries","price":{"amount_minor":6500,"currency":"THB"},"allergens":[],"recipe":"strawberry; sugar","active":true}`

func TestReplaceReplacesStatePreservesIdentityAndIsIdempotent(t *testing.T) {
	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	input := validCreateInput("Original")
	image := "https://example.com/original.png"
	input.ImageURL = &image
	created, err := catalog.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(catalog)
	path := "/api/v1/flavors/" + created.ID.String()
	manager := handlerToken(t, auth.RoleManager)
	first := catalogRequest(t, app, "PUT", path, replacementJSON, manager, 200)
	var replaced model.FlavorAdmin
	if err := json.Unmarshal(first, &replaced); err != nil {
		t.Fatal(err)
	}
	if replaced.ID != created.ID || !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("identity/timestamps changed incorrectly: %+v", replaced)
	}
	if replaced.Name != "Strawberry" || replaced.Price.AmountMinor != 6500 || replaced.ImageURL != "" || len(replaced.Allergens) != 0 || replaced.Recipe != "strawberry; sugar" {
		t.Fatalf("PUT retained old fields: %+v", replaced)
	}
	second := catalogRequest(t, app, "PUT", path, replacementJSON, manager, 200)
	if string(second) != string(first) {
		t.Fatalf("identical PUT changed resource: %s -> %s", first, second)
	}
	public := catalogRequest(t, app, "GET", path, "", "", 200)
	if strings.Contains(string(public), "recipe") {
		t.Fatal("public GET exposed recipe")
	}
	// PATCH remains partial: only the name changes.
	patched := catalogRequest(t, app, "PATCH", path, `{"name":"Berry"}`, manager, 200)
	if !strings.Contains(string(patched), `"amount_minor":6500`) {
		t.Fatalf("PATCH cleared untouched state: %s", patched)
	}
}

func TestReplaceRejectsIncompleteAndInvalidStateWithoutWriting(t *testing.T) {
	catalog := service.NewCatalogService(newMemoryFlavorRepository())
	created, err := catalog.Create(context.Background(), validCreateInput("Original"))
	if err != nil {
		t.Fatal(err)
	}
	app := newCatalogHandlerApp(catalog)
	path := "/api/v1/flavors/" + created.ID.String()
	manager := handlerToken(t, auth.RoleManager)
	var valid map[string]any
	if err := json.Unmarshal([]byte(replacementJSON), &valid); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "description", "price", "allergens", "recipe", "active"} {
		t.Run("missing_"+field, func(t *testing.T) {
			body := make(map[string]any)
			for key, value := range valid {
				if key != field {
					body[key] = value
				}
			}
			raw, _ := json.Marshal(body)
			catalogRequest(t, app, "PUT", path, string(raw), manager, 400)
		})
	}
	for _, body := range []string{
		strings.Replace(replacementJSON, `"amount_minor":6500,`, "", 1),
		strings.Replace(replacementJSON, `6500`, `-1`, 1),
		strings.Replace(replacementJSON, `6500`, `65.5`, 1),
		strings.Replace(replacementJSON, `"THB"`, `"USD"`, 1),
		strings.Replace(replacementJSON, `"active":true`, `"active":null`, 1),
		strings.Replace(replacementJSON, `"active":true`, `"active":true,"available_portions":10`, 1),
	} {
		catalogRequest(t, app, "PUT", path, body, manager, 400)
	}
	stored, err := catalog.Get(context.Background(), created.ID, service.PublicRead)
	if err != nil || stored.Name != "Original" || !stored.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("invalid replacement changed storage: %+v, %v", stored, err)
	}
	catalogRequest(t, app, "PUT", "/api/v1/flavors/not-a-uuid", replacementJSON, manager, 400)
	catalogRequest(t, app, "PUT", "/api/v1/flavors/"+uuid.NewString(), replacementJSON, manager, 404)
	if _, err := catalog.Create(context.Background(), validCreateInput("Strawberry")); err != nil {
		t.Fatal(err)
	}
	catalogRequest(t, app, "PUT", path, replacementJSON, manager, 409)
}

func TestReplaceAndArchiveRequireManager(t *testing.T) {
	app := newCatalogHandlerApp(service.NewCatalogService(newMemoryFlavorRepository()))
	path := "/api/v1/flavors/" + uuid.NewString()
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		catalogRequest(t, app, method, path, replacementJSON, "", 401)
		for _, role := range []auth.Role{auth.RoleCustomer, auth.RoleStaff} {
			catalogRequest(t, app, method, path, replacementJSON, handlerToken(t, role), 403)
		}
	}
}

func TestConcurrentCatalogWriteReturnsConflict(t *testing.T) {
	stub := &stubCatalogService{updateErr: repository.ErrUpdateConflict, archiveErr: repository.ErrUpdateConflict}
	app := newCatalogHandlerApp(stub)
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		payload := catalogRequest(t, app, method, "/api/v1/flavors/"+uuid.NewString(), replacementJSON, handlerToken(t, auth.RoleManager), 409)
		if !strings.Contains(string(payload), "FLAVOR_UPDATE_CONFLICT") {
			t.Fatalf("wrong error: %s", payload)
		}
	}
}

func catalogRequest(t *testing.T, app *fiber.App, method, path, body, token string, status int) []byte {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, path, status, response.StatusCode, payload)
	}
	return payload
}
