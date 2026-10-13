package tests

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"auth-service/internal/auth"
	"auth-service/internal/model"
	"auth-service/internal/repository"
	"auth-service/internal/service"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type fakeUserRepository struct {
	byEmail      map[string]*model.User
	byID         map[uuid.UUID]*model.User
	outboxEvents []*model.OutboxEvent
	createErr    error
}

func newFakeUserRepository() *fakeUserRepository {
	return &fakeUserRepository{
		byEmail:      make(map[string]*model.User),
		byID:         make(map[uuid.UUID]*model.User),
		outboxEvents: make([]*model.OutboxEvent, 0),
	}
}

func (r *fakeUserRepository) CreateWithOutbox(ctx context.Context, user *model.User, event *model.OutboxEvent) error {
	if err := r.Create(ctx, user); err != nil {
		return err
	}
	if event != nil {
		r.outboxEvents = append(r.outboxEvents, event)
	}
	return nil
}

func (r *fakeUserRepository) Create(_ context.Context, user *model.User) error {
	if r.createErr != nil {
		return r.createErr
	}
	if _, exists := r.byEmail[user.Email]; exists {
		return repository.ErrEmailAlreadyExists
	}
	copy := *user
	copy.CreatedAt = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	copy.UpdatedAt = copy.CreatedAt
	*user = copy
	r.byEmail[user.Email] = &copy
	r.byID[user.ID] = &copy
	return nil
}

func (r *fakeUserRepository) FindByEmail(_ context.Context, email string) (*model.User, error) {
	user, exists := r.byEmail[email]
	if !exists {
		return nil, repository.ErrUserNotFound
	}
	copy := *user
	return &copy, nil
}

func (r *fakeUserRepository) FindByID(_ context.Context, id uuid.UUID) (*model.User, error) {
	user, exists := r.byID[id]
	if !exists {
		return nil, repository.ErrUserNotFound
	}
	copy := *user
	return &copy, nil
}

func newService(repo service.UserRepository) (service.AuthService, *auth.TokenManager) {
	tokens := auth.NewTokenManager(
		"test_secret_that_is_longer_than_32_bytes",
		"gelatoflow-auth",
		"gelatoflow-api",
		15*time.Minute,
	)
	svc, err := service.NewAuthService(repo, tokens, bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return svc, tokens
}

func TestSignUpNormalizesIdentityAndForcesCustomerRole(t *testing.T) {
	repo := newFakeUserRepository()
	svc, _ := newService(repo)

	user, err := svc.SignUp(context.Background(), service.SignUpInput{
		Email:       "  USER@Example.COM ",
		Password:    "correct-horse-battery-staple",
		DisplayName: "  Gelato Fan  ",
	})
	if err != nil {
		t.Fatalf("SignUp returned error: %v", err)
	}
	if user.Email != "user@example.com" {
		t.Fatalf("expected normalized email, got %q", user.Email)
	}
	if user.DisplayName != "Gelato Fan" {
		t.Fatalf("expected trimmed display name, got %q", user.DisplayName)
	}
	if user.Role != model.RoleCustomer {
		t.Fatalf("public signup assigned role %q", user.Role)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("correct-horse-battery-staple")) != nil {
		t.Fatal("stored password hash does not match the password")
	}

	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshal user: %v", err)
	}
	if string(encoded) == "" || contains(string(encoded), "password") || contains(string(encoded), "correct-horse") {
		t.Fatalf("user JSON leaked password data: %s", encoded)
	}
}

func TestSignUpRejectsDuplicateEmail(t *testing.T) {
	repo := newFakeUserRepository()
	svc, _ := newService(repo)
	input := service.SignUpInput{Email: "user@example.com", Password: "password123", DisplayName: "User"}

	if _, err := svc.SignUp(context.Background(), input); err != nil {
		t.Fatalf("first SignUp returned error: %v", err)
	}
	if _, err := svc.SignUp(context.Background(), input); !errors.Is(err, service.ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
}

func TestSignUpValidatesInput(t *testing.T) {
	tests := []struct {
		name  string
		input service.SignUpInput
	}{
		{name: "invalid email", input: service.SignUpInput{Email: "not-an-email", Password: "password123", DisplayName: "User"}},
		{name: "short password", input: service.SignUpInput{Email: "user@example.com", Password: "short", DisplayName: "User"}},
		{name: "empty display name", input: service.SignUpInput{Email: "user@example.com", Password: "password123", DisplayName: "  "}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := newService(newFakeUserRepository())
			if _, err := svc.SignUp(context.Background(), test.input); !errors.Is(err, service.ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestLoginReturnsContractTokenAndClaims(t *testing.T) {
	repo := newFakeUserRepository()
	svc, tokens := newService(repo)
	created, err := svc.SignUp(context.Background(), service.SignUpInput{
		Email:       "user@example.com",
		Password:    "password123",
		DisplayName: "User",
	})
	if err != nil {
		t.Fatalf("SignUp returned error: %v", err)
	}

	result, err := svc.Login(context.Background(), service.LoginInput{Email: "USER@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if result.TokenType != "Bearer" || result.ExpiresIn != 900 {
		t.Fatalf("unexpected token metadata: %+v", result)
	}
	claims, err := tokens.Parse(result.AccessToken)
	if err != nil {
		t.Fatalf("Parse token returned error: %v", err)
	}
	if claims.Subject != created.ID.String() || claims.Role != model.RoleCustomer {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestLoginUsesOneGenericCredentialError(t *testing.T) {
	repo := newFakeUserRepository()
	svc, _ := newService(repo)
	_, err := svc.SignUp(context.Background(), service.SignUpInput{
		Email:       "user@example.com",
		Password:    "password123",
		DisplayName: "User",
	})
	if err != nil {
		t.Fatalf("SignUp returned error: %v", err)
	}

	attempts := []service.LoginInput{
		{Email: "missing@example.com", Password: "password123"},
		{Email: "user@example.com", Password: "wrong-password"},
	}
	for _, input := range attempts {
		if _, err := svc.Login(context.Background(), input); !errors.Is(err, service.ErrInvalidCredentials) {
			t.Fatalf("expected generic credential error, got %v", err)
		}
	}
}

func TestDisabledUserCannotUseCurrentIdentity(t *testing.T) {
	repo := newFakeUserRepository()
	svc, _ := newService(repo)
	user, err := svc.SignUp(context.Background(), service.SignUpInput{
		Email:       "user@example.com",
		Password:    "password123",
		DisplayName: "User",
	})
	if err != nil {
		t.Fatalf("SignUp returned error: %v", err)
	}
	repo.byID[user.ID].Status = model.UserStatusDisabled

	if _, err := svc.GetCurrentUser(context.Background(), user.ID); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("expected disabled account to be rejected, got %v", err)
	}
}

func TestMissingUserCannotUseCurrentIdentity(t *testing.T) {
	svc, _ := newService(newFakeUserRepository())

	if _, err := svc.GetCurrentUser(context.Background(), uuid.New()); !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("expected missing account to be rejected, got %v", err)
	}
}

func TestSignUpTransactionalOutboxEvent(t *testing.T) {
	repo := newFakeUserRepository()
	svc, _ := newService(repo)

	user, err := svc.SignUp(context.Background(), service.SignUpInput{
		Email:       "outbox@example.com",
		Password:    "password123",
		DisplayName: "Outbox Test",
	})
	if err != nil {
		t.Fatalf("SignUp failed: %v", err)
	}

	if len(repo.outboxEvents) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(repo.outboxEvents))
	}

	event := repo.outboxEvents[0]
	if event.EventType != "user.registered" {
		t.Errorf("expected event_type 'user.registered', got %s", event.EventType)
	}

	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload["email"] != "outbox@example.com" {
		t.Errorf("expected email 'outbox@example.com', got %v", payload["email"])
	}
	if payload["user_id"] != user.ID.String() {
		t.Errorf("expected user_id %s, got %v", user.ID, payload["user_id"])
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
