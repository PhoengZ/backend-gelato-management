package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"auth-service/internal/auth"
	"auth-service/internal/model"
	"auth-service/internal/repository"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type identityRepo struct{ user *model.User }

func (r identityRepo) Create(context.Context, *model.User) error { return nil }
func (r identityRepo) FindByEmail(context.Context, string) (*model.User, error) {
	if r.user == nil {
		return nil, repository.ErrUserNotFound
	}
	return r.user, nil
}
func (r identityRepo) FindByID(context.Context, uuid.UUID) (*model.User, error) { return r.user, nil }

func TestMissingAndDisabledIdentitiesPerformConfiguredPasswordWork(t *testing.T) {
	tokens := auth.NewTokenManager("test_secret_that_is_longer_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", time.Minute)
	for _, user := range []*model.User{nil, {Status: model.UserStatusDisabled}} {
		instance, err := NewAuthService(identityRepo{user}, tokens, bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		svc := instance.(*authService)
		calls := 0
		svc.comparePassword = func(hash, password []byte) error {
			calls++
			cost, err := bcrypt.Cost(hash)
			if err != nil || cost != bcrypt.MinCost {
				t.Fatalf("dummy hash cost=%d err=%v", cost, err)
			}
			if string(password) != "password123" {
				t.Fatal("wrong password reached comparison")
			}
			return bcrypt.CompareHashAndPassword(hash, password)
		}
		if _, err := svc.Login(context.Background(), LoginInput{Email: "user@example.com", Password: "password123"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("unexpected failure: %v", err)
		}
		if calls != 1 {
			t.Fatalf("password comparisons=%d, expected1", calls)
		}
	}
}

func TestPasswordCharacterMinimumAndByteMaximum(t *testing.T) {
	for _, password := range []string{"กกก", "1234567", strings.Repeat("ก", 25), strings.Repeat("a", 73)} {
		if validatePassword(password, 8) == nil {
			t.Fatalf("invalid password accepted: %q", password)
		}
	}
	for _, password := range []string{strings.Repeat("ก", 8), strings.Repeat("a", 72), strings.Repeat("ก", 24)} {
		if err := validatePassword(password, 8); err != nil {
			t.Fatalf("valid password rejected: %v", err)
		}
	}
	if _, err := NewAuthService(identityRepo{}, nil, 32); err == nil {
		t.Fatal("unsafe bcrypt cost accepted")
	}
}

func TestCorruptStoredHashIsAnOperationalError(t *testing.T) {
	tokens := auth.NewTokenManager("test_secret_that_is_longer_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", time.Minute)
	user := &model.User{ID: uuid.New(), Email: "user@example.com", Role: model.RoleCustomer, Status: model.UserStatusActive, PasswordHash: "corrupt-hash"}
	svc, err := NewAuthService(identityRepo{user}, tokens, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(context.Background(), LoginInput{Email: user.Email, Password: "correct-password"}); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("corrupt storage was counted as a bad password: %v", err)
	}
}
