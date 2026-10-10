package auth

import (
	"errors"
	"testing"
	"time"

	"auth-service/internal/model"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testTokenSecret = "test_secret_that_is_longer_than_32_bytes"

func TestIssueAndParseRequiredClaims(t *testing.T) {
	manager := newTestTokenManager()
	user := &model.User{
		ID:     uuid.MustParse("76ea43cb-42fa-44c8-bb0f-168769632a7d"),
		Role:   model.RoleCustomer,
		Status: model.UserStatusActive,
	}

	raw, err := manager.Issue(user)
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	claims, err := manager.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if claims.Subject != user.ID.String() || claims.Role != user.Role {
		t.Fatalf("unexpected identity claims: %+v", claims)
	}
	if claims.Issuer != "gelatoflow-auth" ||
		len(claims.Audience) != 1 || claims.Audience[0] != "gelatoflow-api" ||
		claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatalf("required registered claims are missing: %+v", claims)
	}
}

func TestParseRejectsInvalidRequiredClaims(t *testing.T) {
	manager := newTestTokenManager()
	now := manager.now().UTC()
	valid := Claims{
		Role: model.RoleCustomer,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "76ea43cb-42fa-44c8-bb0f-168769632a7d",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}

	tests := []struct {
		name   string
		mutate func(*Claims)
	}{
		{name: "missing issued at", mutate: func(claims *Claims) { claims.IssuedAt = nil }},
		{name: "missing expiry", mutate: func(claims *Claims) { claims.ExpiresAt = nil }},
		{name: "invalid issuer", mutate: func(claims *Claims) { claims.Issuer = "other-auth" }},
		{name: "invalid audience", mutate: func(claims *Claims) { claims.Audience = jwt.ClaimStrings{"other-api"} }},
		{name: "invalid subject", mutate: func(claims *Claims) { claims.Subject = "usr_123" }},
		{name: "invalid role", mutate: func(claims *Claims) { claims.Role = model.Role("OWNER") }},
		{name: "issued too far in future", mutate: func(claims *Claims) { claims.IssuedAt = jwt.NewNumericDate(now.Add(time.Minute)) }},
		{name: "expiry before issue", mutate: func(claims *Claims) { claims.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute)) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := valid
			claims.Audience = append(jwt.ClaimStrings(nil), valid.Audience...)
			test.mutate(&claims)
			raw := signTestClaims(t, claims)
			if _, err := manager.Parse(raw); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected ErrInvalidToken, got %v", err)
			}
		})
	}
}

func TestParseRejectsUnexpectedSigningAlgorithm(t *testing.T) {
	manager := newTestTokenManager()
	now := manager.now().UTC()
	claims := Claims{
		Role: model.RoleCustomer,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "76ea43cb-42fa-44c8-bb0f-168769632a7d",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS384, claims)
	raw, err := token.SignedString([]byte(testTokenSecret))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}

	if _, err := manager.Parse(raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func newTestTokenManager() *TokenManager {
	manager := NewTokenManager(
		testTokenSecret,
		"gelatoflow-auth",
		"gelatoflow-api",
		15*time.Minute,
	)
	manager.now = func() time.Time {
		return time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	}
	return manager
}

func signTestClaims(t *testing.T, claims Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	raw, err := token.SignedString([]byte(testTokenSecret))
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return raw
}
