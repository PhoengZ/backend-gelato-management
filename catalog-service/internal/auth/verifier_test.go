package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test_secret_that_is_longer_than_32_bytes"

func TestVerifierAcceptsManagerToken(t *testing.T) {
	verifier := NewVerifier(testSecret, "gelatoflow-auth", "gelatoflow-api")
	raw := signTestToken(t, RoleManager, "gelatoflow-auth", "gelatoflow-api", time.Now().Add(time.Minute))

	claims, err := verifier.Parse(raw)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if claims.Role != RoleManager {
		t.Fatalf("expected manager role, got %s", claims.Role)
	}
}

func TestVerifierRejectsInvalidClaims(t *testing.T) {
	verifier := NewVerifier(testSecret, "gelatoflow-auth", "gelatoflow-api")
	cases := []string{
		signTestToken(t, Role("OWNER"), "gelatoflow-auth", "gelatoflow-api", time.Now().Add(time.Minute)),
		signTestToken(t, RoleManager, "wrong-issuer", "gelatoflow-api", time.Now().Add(time.Minute)),
		signTestToken(t, RoleManager, "gelatoflow-auth", "wrong-audience", time.Now().Add(time.Minute)),
		signTestToken(t, RoleManager, "gelatoflow-auth", "gelatoflow-api", time.Now().Add(-time.Minute)),
	}
	for _, raw := range cases {
		if _, err := verifier.Parse(raw); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("expected invalid token error, got %v", err)
		}
	}
	if _, err := verifier.Parse(signTestTokenWithoutIssuedAt(t)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected token without issued-at claim to fail, got %v", err)
	}
}

func signTestToken(t *testing.T, role Role, issuer, audience string, expiresAt time.Time) string {
	t.Helper()
	now := time.Now().UTC()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "76ea43cb-42fa-44c8-bb0f-168769632a7d",
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return raw
}

func signTestTokenWithoutIssuedAt(t *testing.T) string {
	t.Helper()
	claims := Claims{
		Role: RoleManager,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "76ea43cb-42fa-44c8-bb0f-168769632a7d",
			Issuer:    "gelatoflow-auth",
			Audience:  jwt.ClaimStrings{"gelatoflow-api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token without issued-at: %v", err)
	}
	return raw
}
