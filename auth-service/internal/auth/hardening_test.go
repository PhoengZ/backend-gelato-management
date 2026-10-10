package auth

import (
	"errors"
	"testing"
	"time"

	"auth-service/internal/model"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestIssueRefusesInvalidAccountIdentity(t *testing.T) {
	valid := model.User{
		ID:     uuid.MustParse("76ea43cb-42fa-44c8-bb0f-168769632a7d"),
		Role:   model.RoleCustomer,
		Status: model.UserStatusActive,
	}
	for _, tc := range []struct {
		name   string
		mutate func(*model.User) *model.User
	}{
		{"nil user", func(_ *model.User) *model.User { return nil }},
		{"nil uuid", func(u *model.User) *model.User { u.ID = uuid.Nil; return u }},
		{"disabled account", func(u *model.User) *model.User { u.Status = model.UserStatusDisabled; return u }},
		{"missing status", func(u *model.User) *model.User { u.Status = ""; return u }},
		{"unknown role", func(u *model.User) *model.User { u.Role = "OWNER"; return u }},
		{"missing role", func(u *model.User) *model.User { u.Role = ""; return u }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := valid
			raw, err := newTestTokenManager().Issue(tc.mutate(&user))
			if err == nil || raw != "" {
				t.Fatalf("invalid identity received a token: token present=%t err=%v", raw != "", err)
			}
		})
	}
	for _, role := range []model.Role{model.RoleCustomer, model.RoleStaff, model.RoleManager} {
		t.Run(string(role), func(t *testing.T) {
			user := valid
			user.Role = role
			manager := newTestTokenManager()
			raw, err := manager.Issue(&user)
			if err != nil {
				t.Fatalf("issue valid active identity: %v", err)
			}
			claims, err := manager.Parse(raw)
			if err != nil || claims.Role != role || claims.Subject != user.ID.String() {
				t.Fatalf("valid identity did not round trip: claims=%+v err=%v", claims, err)
			}
		})
	}
}

func TestParseEnforcesExactLifetimeAndIdentityBoundary(t *testing.T) {
	manager := newTestTokenManager()
	now := manager.now().UTC()
	valid := Claims{
		Role: model.RoleCustomer,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "76ea43cb-42fa-44c8-bb0f-168769632a7d",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Claims)
	}{
		{"nil uuid", func(c *Claims) { c.Subject = uuid.Nil.String() }},
		{"missing subject", func(c *Claims) { c.Subject = "" }},
		{"missing role", func(c *Claims) { c.Role = "" }},
		{"expires exactly now", func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now) }},
		{"expired one second ago", func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second)) }},
		{"issued one second ahead", func(c *Claims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Second)) }},
		{"not valid until next second", func(c *Claims) { c.NotBefore = jwt.NewNumericDate(now.Add(time.Second)) }},
		{"zero lifetime", func(c *Claims) { c.ExpiresAt = c.IssuedAt }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := valid
			tc.mutate(&claims)
			if _, err := manager.Parse(signTestClaims(t, claims)); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("expected invalid-token error, got %v", err)
			}
		})
	}
	valid.ExpiresAt = jwt.NewNumericDate(now.Add(time.Second))
	if _, err := manager.Parse(signTestClaims(t, valid)); err != nil {
		t.Fatalf("token valid for one more second was rejected: %v", err)
	}
	for _, raw := range []string{"", "not-a-token", "eyJhbGciOiJIUzI1NiJ9.e30.invalid"} {
		if _, err := manager.Parse(raw); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("malformed token %q returned %v", raw, err)
		}
	}
}
