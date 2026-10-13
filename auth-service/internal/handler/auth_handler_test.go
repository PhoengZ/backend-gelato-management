package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"auth-service/internal/audit"
	"auth-service/internal/middleware"
	"auth-service/internal/model"
	"auth-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type mockAuthService struct {
	signUpFunc       func(ctx context.Context, input service.SignUpInput) (*model.User, error)
	loginFunc        func(ctx context.Context, input service.LoginInput) (*service.AuthResult, error)
	getCurrentUserFn func(ctx context.Context, id uuid.UUID) (*model.User, error)
}

func (m *mockAuthService) SignUp(ctx context.Context, input service.SignUpInput) (*model.User, error) {
	if m.signUpFunc != nil {
		return m.signUpFunc(ctx, input)
	}
	return nil, nil
}

func (m *mockAuthService) Login(ctx context.Context, input service.LoginInput) (*service.AuthResult, error) {
	if m.loginFunc != nil {
		return m.loginFunc(ctx, input)
	}
	return nil, nil
}

func (m *mockAuthService) GetCurrentUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	if m.getCurrentUserFn != nil {
		return m.getCurrentUserFn(ctx, id)
	}
	return nil, nil
}

func setupTestApp(svc service.AuthService, rateLimiter *middleware.RateLimiter, auditLogger audit.AuditLogger) *fiber.App {
	limiter := middleware.NewCredentialLimiter()
	h := NewAuthHandler(svc, limiter, rateLimiter, auditLogger)

	app := fiber.New()
	app.Post("/signup", middleware.RequireCredentialJSON, h.SignUp)
	app.Post("/login", middleware.RequireCredentialJSON, h.Login)
	return app
}

func TestAuthHandlerSignUpAuditAndRateLimiter(t *testing.T) {
	auditLogger := audit.NewInMemoryAuditLogger()
	rateLimiter := middleware.NewRateLimiter(5, 10*time.Minute)
	userID := uuid.New()

	svc := &mockAuthService{
		signUpFunc: func(ctx context.Context, input service.SignUpInput) (*model.User, error) {
			return &model.User{
				ID:          userID,
				Email:       input.Email,
				DisplayName: input.DisplayName,
				Role:        model.RoleCustomer,
				Status:      model.UserStatusActive,
			}, nil
		},
	}

	app := setupTestApp(svc, rateLimiter, auditLogger)

	body := `{"email":"test@example.com","password":"password123","display_name":"Test User"}`
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TestClient/1.0")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", resp.StatusCode)
	}

	records := auditLogger.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	if records[0].EventType != audit.EventUserRegistered {
		t.Fatalf("expected event %s, got %s", audit.EventUserRegistered, records[0].EventType)
	}
	if *records[0].UserID != userID {
		t.Fatalf("expected user ID %s, got %s", userID, records[0].UserID)
	}
}

func TestAuthHandlerLoginSuccessAudit(t *testing.T) {
	auditLogger := audit.NewInMemoryAuditLogger()
	rateLimiter := middleware.NewRateLimiter(5, 10*time.Minute)
	userID := uuid.New()

	svc := &mockAuthService{
		loginFunc: func(ctx context.Context, input service.LoginInput) (*service.AuthResult, error) {
			return &service.AuthResult{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   900,
				User: &model.User{
					ID:    userID,
					Email: input.Email,
					Role:  model.RoleCustomer,
				},
			}, nil
		},
	}

	app := setupTestApp(svc, rateLimiter, auditLogger)

	body := `{"email":"test@example.com","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TestClient/1.0")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	records := auditLogger.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(records))
	}
	if records[0].EventType != audit.EventLoginSuccess {
		t.Fatalf("expected event %s, got %s", audit.EventLoginSuccess, records[0].EventType)
	}
	if *records[0].UserID != userID {
		t.Fatalf("expected user ID %s, got %s", userID, records[0].UserID)
	}
}

func TestAuthHandlerLoginRateLimitBruteForce(t *testing.T) {
	auditLogger := audit.NewInMemoryAuditLogger()
	rateLimiter := middleware.NewRateLimiter(5, 10*time.Minute)

	svc := &mockAuthService{
		loginFunc: func(ctx context.Context, input service.LoginInput) (*service.AuthResult, error) {
			return nil, service.ErrInvalidCredentials
		},
	}

	app := setupTestApp(svc, rateLimiter, auditLogger)

	body := `{"email":"brute@example.com","password":"wrongpassword"}`

	// 5 failed login attempts
	for i := 1; i <= 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("attempt %d failed: %v", i, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i, resp.StatusCode)
		}
	}

	// Verify 5 audit logs recorded
	records := auditLogger.Records()
	if len(records) != 5 {
		t.Fatalf("expected 5 LOGIN_FAILED audit records, got %d", len(records))
	}

	// 6th attempt should be blocked with 429
	req6 := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(body))
	req6.Header.Set("Content-Type", "application/json")
	resp6, err := app.Test(req6)
	if err != nil {
		t.Fatalf("6th attempt error: %v", err)
	}
	defer resp6.Body.Close()

	if resp6.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", resp6.StatusCode)
	}
	if resp6.Header.Get(fiber.HeaderRetryAfter) == "" {
		t.Fatal("expected Retry-After header on 429")
	}

	var errBody map[string]any
	if err := json.NewDecoder(resp6.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if errBody["code"] != "RATE_LIMITED" {
		t.Fatalf("expected code RATE_LIMITED, got %v", errBody["code"])
	}
}
