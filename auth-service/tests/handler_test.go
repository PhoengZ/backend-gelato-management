package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"auth-service/internal/auth"
	"auth-service/internal/model"
	"auth-service/internal/router"
	"auth-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type stubAuthService struct {
	signUpResult *model.User
	signUpErr    error
	loginResult  *service.AuthResult
	loginErr     error
	meResult     *model.User
	meErr        error
	signUpCalls  int
}

func (s *stubAuthService) SignUp(_ context.Context, _ service.SignUpInput) (*model.User, error) {
	s.signUpCalls++
	return s.signUpResult, s.signUpErr
}

func (s *stubAuthService) Login(_ context.Context, _ service.LoginInput) (*service.AuthResult, error) {
	return s.loginResult, s.loginErr
}

func (s *stubAuthService) GetCurrentUser(_ context.Context, _ uuid.UUID) (*model.User, error) {
	return s.meResult, s.meErr
}

func newHandlerApp(stub service.AuthService) (*fiber.App, *auth.TokenManager) {
	tokens := auth.NewTokenManager(
		"test_secret_that_is_longer_than_32_bytes",
		"gelatoflow-auth",
		"gelatoflow-api",
		15*time.Minute,
	)
	app := fiber.New()
	router.Setup(app, stub, tokens)
	return app, tokens
}

func TestSignUpRejectsRoleInjection(t *testing.T) {
	stub := &stubAuthService{}
	app, _ := newHandlerApp(stub)
	request := httptest.NewRequest(
		"POST",
		"/api/v1/auth/signup",
		strings.NewReader(`{"email":"user@example.com","password":"password123","display_name":"User","role":"MANAGER"}`),
	)
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.StatusCode)
	}
	if stub.signUpCalls != 0 {
		t.Fatal("service was called for a request containing an unknown role field")
	}
}

func TestLoginResponseMatchesContract(t *testing.T) {
	user := testUser()
	stub := &stubAuthService{loginResult: &service.AuthResult{
		AccessToken: "signed-token",
		TokenType:   "Bearer",
		ExpiresIn:   900,
		User:        user,
	}}
	app, _ := newHandlerApp(stub)
	request := httptest.NewRequest(
		"POST",
		"/api/v1/auth/login",
		strings.NewReader(`{"email":"user@example.com","password":"password123"}`),
	)
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["access_token"] != "signed-token" || payload["token_type"] != "Bearer" {
		t.Fatalf("unexpected login response: %+v", payload)
	}
}

func TestMeRequiresAndAcceptsBearerToken(t *testing.T) {
	user := testUser()
	stub := &stubAuthService{meResult: user}
	app, tokens := newHandlerApp(stub)

	missingRequest := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	missingResponse, err := app.Test(missingRequest)
	if err != nil {
		t.Fatalf("missing-token request failed: %v", err)
	}
	missingResponse.Body.Close()
	if missingResponse.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected missing token to return 401, got %d", missingResponse.StatusCode)
	}

	token, err := tokens.Issue(user)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	authorizedRequest := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer "+token)
	authorizedResponse, err := app.Test(authorizedRequest)
	if err != nil {
		t.Fatalf("authorized request failed: %v", err)
	}
	defer authorizedResponse.Body.Close()
	if authorizedResponse.StatusCode != fiber.StatusOK {
		t.Fatalf("expected authorized request to return 200, got %d", authorizedResponse.StatusCode)
	}
}

func TestMeDoesNotTrustClientIdentityHeaders(t *testing.T) {
	user := testUser()
	stub := &stubAuthService{meResult: user}
	app, _ := newHandlerApp(stub)
	request := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	request.Header.Set("X-Gelato-User-ID", user.ID.String())
	request.Header.Set("X-Gelato-User-Role", string(user.Role))

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected identity headers without bearer token to return 401, got %d", response.StatusCode)
	}
	assertErrorCode(t, response, "UNAUTHORIZED")
}

func TestMeUsesUnauthorizedErrorForInactiveIdentity(t *testing.T) {
	user := testUser()
	stub := &stubAuthService{meErr: service.ErrUnauthorized}
	app, tokens := newHandlerApp(stub)
	token, err := tokens.Issue(user)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	request := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected inactive identity to return 401, got %d", response.StatusCode)
	}
	assertErrorCode(t, response, "UNAUTHORIZED")
}

func assertErrorCode(t *testing.T, response *http.Response, expected string) {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload["code"] != expected {
		t.Fatalf("expected error code %q, got %+v", expected, payload)
	}
}

func testUser() *model.User {
	return &model.User{
		ID:          uuid.MustParse("76ea43cb-42fa-44c8-bb0f-168769632a7d"),
		Email:       "user@example.com",
		DisplayName: "User",
		Role:        model.RoleCustomer,
		Status:      model.UserStatusActive,
		CreatedAt:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
}
