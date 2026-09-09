package tests

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"catalog-service/internal/auth"
	"catalog-service/internal/model"
	"catalog-service/internal/router"
	"catalog-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const handlerTestSecret = "handler_test_secret_that_exceeds_32_bytes"

type stubCatalogService struct {
	createResult *model.FlavorAdmin
	createErr    error
	listResult   []model.Flavor
	listErr      error
	getResult    *model.Flavor
	getErr       error
	updateResult *model.FlavorAdmin
	updateErr    error
	archiveErr   error
	recipeResult *model.FlavorRecipe
	recipeErr    error
	createCalls  int
}

func (s *stubCatalogService) Create(_ context.Context, _ service.CreateFlavorInput) (*model.FlavorAdmin, error) {
	s.createCalls++
	return s.createResult, s.createErr
}

func (s *stubCatalogService) List(_ context.Context, _ *bool) ([]model.Flavor, error) {
	return s.listResult, s.listErr
}

func (s *stubCatalogService) Get(_ context.Context, _ uuid.UUID) (*model.Flavor, error) {
	return s.getResult, s.getErr
}

func (s *stubCatalogService) Update(_ context.Context, _ uuid.UUID, _ service.UpdateFlavorInput) (*model.FlavorAdmin, error) {
	return s.updateResult, s.updateErr
}

func (s *stubCatalogService) Replace(_ context.Context, _ uuid.UUID, _ service.ReplaceFlavorInput) (*model.FlavorAdmin, error) {
	return s.updateResult, s.updateErr
}

func (s *stubCatalogService) Archive(_ context.Context, _ uuid.UUID) error {
	return s.archiveErr
}

func (s *stubCatalogService) GetRecipe(_ context.Context, _ uuid.UUID) (*model.FlavorRecipe, error) {
	return s.recipeResult, s.recipeErr
}

func newCatalogHandlerApp(stub service.CatalogService) *fiber.App {
	app := fiber.New()
	verifier := auth.NewVerifier(handlerTestSecret, "gelatoflow-auth", "gelatoflow-api")
	router.Setup(app, stub, verifier, time.Second)
	return app
}

func TestPublicCatalogListDoesNotRequireAuthentication(t *testing.T) {
	flavor := testFlavor().Flavor
	stub := &stubCatalogService{listResult: []model.Flavor{flavor}}
	app := newCatalogHandlerApp(stub)

	request := httptest.NewRequest("GET", "/api/v1/flavors?active=true", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected one flavor, got %+v", payload.Items)
	}
	if _, leaked := payload.Items[0]["recipe"]; leaked {
		t.Fatal("public catalog response leaked recipe")
	}
}

func TestManagerAuthorizationProtectsCatalogWrites(t *testing.T) {
	created := testFlavor()
	stub := &stubCatalogService{createResult: created}
	app := newCatalogHandlerApp(stub)
	body := `{"name":"Vanilla","description":"Classic","price":{"amount_minor":10000,"currency":"THB"},"allergens":["MILK"],"recipe":"milk; sugar"}`

	missing := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(body))
	missing.Header.Set("Content-Type", "application/json")
	missingResponse, err := app.Test(missing)
	if err != nil {
		t.Fatalf("missing-token request failed: %v", err)
	}
	missingResponse.Body.Close()
	if missingResponse.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected missing token to return 401, got %d", missingResponse.StatusCode)
	}

	customer := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(body))
	customer.Header.Set("Authorization", "Bearer "+handlerToken(t, auth.RoleCustomer))
	customer.Header.Set("Content-Type", "application/json")
	customerResponse, err := app.Test(customer)
	if err != nil {
		t.Fatalf("customer request failed: %v", err)
	}
	customerResponse.Body.Close()
	if customerResponse.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected customer token to return 403, got %d", customerResponse.StatusCode)
	}

	manager := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(body))
	manager.Header.Set("Authorization", "Bearer "+handlerToken(t, auth.RoleManager))
	manager.Header.Set("Content-Type", "application/json")
	managerResponse, err := app.Test(manager)
	if err != nil {
		t.Fatalf("manager request failed: %v", err)
	}
	defer managerResponse.Body.Close()
	if managerResponse.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected manager token to return 201, got %d", managerResponse.StatusCode)
	}
}

func TestCreateRejectsUnknownAndNullProperties(t *testing.T) {
	stub := &stubCatalogService{}
	app := newCatalogHandlerApp(stub)
	managerToken := handlerToken(t, auth.RoleManager)
	cases := []string{
		`{"name":"Vanilla","description":"Classic","price":{"amount_minor":10000,"currency":"THB"},"allergens":[],"recipe":"milk","available_portions":10}`,
		`{"name":"Vanilla","description":null,"price":{"amount_minor":10000,"currency":"THB"},"allergens":[],"recipe":"milk"}`,
		`{"name":"Vanilla","description":"Classic","price":{"amount_minor":null,"currency":"THB"},"allergens":[],"recipe":"milk"}`,
	}
	for _, body := range cases {
		request := httptest.NewRequest("POST", "/api/v1/flavors", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+managerToken)
		request.Header.Set("Content-Type", "application/json")
		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("expected 400, got %d for %s", response.StatusCode, body)
		}
	}
	if stub.createCalls != 0 {
		t.Fatalf("service was called %d times for invalid requests", stub.createCalls)
	}
}

func TestRecipeEndpointRequiresManager(t *testing.T) {
	flavor := testFlavor()
	stub := &stubCatalogService{recipeResult: &model.FlavorRecipe{
		FlavorID: flavor.ID, Recipe: flavor.Recipe, UpdatedAt: flavor.UpdatedAt,
	}}
	app := newCatalogHandlerApp(stub)
	path := "/api/v1/flavors/" + flavor.ID.String() + "/recipe"

	staffRequest := httptest.NewRequest("GET", path, nil)
	staffRequest.Header.Set("Authorization", "Bearer "+handlerToken(t, auth.RoleStaff))
	staffResponse, err := app.Test(staffRequest)
	if err != nil {
		t.Fatalf("staff request failed: %v", err)
	}
	staffResponse.Body.Close()
	if staffResponse.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected staff token to return 403, got %d", staffResponse.StatusCode)
	}

	managerRequest := httptest.NewRequest("GET", path, nil)
	managerRequest.Header.Set("Authorization", "Bearer "+handlerToken(t, auth.RoleManager))
	managerResponse, err := app.Test(managerRequest)
	if err != nil {
		t.Fatalf("manager request failed: %v", err)
	}
	managerResponse.Body.Close()
	if managerResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("expected manager token to return 200, got %d", managerResponse.StatusCode)
	}
}

func handlerToken(t *testing.T, role auth.Role) string {
	t.Helper()
	now := time.Now().UTC()
	claims := auth.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "76ea43cb-42fa-44c8-bb0f-168769632a7d",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(handlerTestSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

func testFlavor() *model.FlavorAdmin {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	return &model.FlavorAdmin{
		Flavor: model.Flavor{
			ID:          uuid.MustParse("0f3bca11-1eb2-4a86-908a-e60d7656c79b"),
			Name:        "Vanilla",
			Description: "Classic vanilla",
			Price:       model.Money{AmountMinor: 10000, Currency: "THB"},
			Allergens:   []model.Allergen{model.AllergenMilk},
			Active:      true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Recipe: "milk; sugar; vanilla",
	}
}
