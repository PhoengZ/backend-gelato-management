package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	testSecret   = "super-secret-signing-key-for-test-32b"
	testIssuer   = "gelatoflow-auth"
	testAudience = "gelatoflow-api"
)

func makeTestToken(t *testing.T, secret, issuer, audience, sub string, role Role, iat, exp time.Time) string {
	t.Helper()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(iat),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}

func TestVerifierParseValid(t *testing.T) {
	v := NewVerifier(testSecret, testIssuer, testAudience)
	now := time.Now().UTC()
	sub := uuid.New().String()

	token := makeTestToken(t, testSecret, testIssuer, testAudience, sub, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour))
	claims, err := v.Parse(token)
	if err != nil {
		t.Fatalf("expected valid token, got err: %v", err)
	}
	if claims.Subject != sub {
		t.Errorf("subject = %s, want %s", claims.Subject, sub)
	}
	if claims.Role != RoleStaff {
		t.Errorf("role = %s, want %s", claims.Role, RoleStaff)
	}
}

func TestVerifierParseRejections(t *testing.T) {
	v := NewVerifier(testSecret, testIssuer, testAudience)
	now := time.Now().UTC()
	sub := uuid.New().String()

	cases := []struct {
		name     string
		secret   string
		issuer   string
		audience string
		sub      string
		role     Role
		iat      time.Time
		exp      time.Time
	}{
		{"expired", testSecret, testIssuer, testAudience, sub, RoleStaff, now.Add(-2 * time.Hour), now.Add(-time.Hour)},
		{"wrong secret", "wrong-secret-signing-key-12345678", testIssuer, testAudience, sub, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour)},
		{"wrong issuer", testSecret, "wrong-issuer", testAudience, sub, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour)},
		{"wrong audience", testSecret, testIssuer, "wrong-audience", sub, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour)},
		{"invalid uuid sub", testSecret, testIssuer, testAudience, "not-a-uuid", RoleStaff, now.Add(-time.Minute), now.Add(time.Hour)},
		{"empty sub", testSecret, testIssuer, testAudience, uuid.Nil.String(), RoleStaff, now.Add(-time.Minute), now.Add(time.Hour)},
		{"invalid role", testSecret, testIssuer, testAudience, sub, Role("SUPERADMIN"), now.Add(-time.Minute), now.Add(time.Hour)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := makeTestToken(t, tc.secret, tc.issuer, tc.audience, tc.sub, tc.role, tc.iat, tc.exp)
			if _, err := v.Parse(token); err == nil {
				t.Fatalf("expected token parse to fail for %s", tc.name)
			}
		})
	}

	t.Run("malformed raw token", func(t *testing.T) {
		if _, err := v.Parse("invalid.token.string"); err == nil {
			t.Fatal("expected failure on malformed token")
		}
	})

	t.Run("empty secret verifier", func(t *testing.T) {
		emptyVerifier := NewVerifier("", testIssuer, testAudience)
		token := makeTestToken(t, testSecret, testIssuer, testAudience, sub, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour))
		if _, err := emptyVerifier.Parse(token); err == nil {
			t.Fatal("expected failure with empty verifier secret")
		}
	})
}

func TestAuthMiddlewareRequireStaffOrManager(t *testing.T) {
	v := NewVerifier(testSecret, testIssuer, testAudience)
	authMid := NewAuthMiddleware(v)
	now := time.Now().UTC()

	customerID := uuid.New().String()
	staffID := uuid.New().String()
	managerID := uuid.New().String()

	customerToken := makeTestToken(t, testSecret, testIssuer, testAudience, customerID, RoleCustomer, now.Add(-time.Minute), now.Add(time.Hour))
	staffToken := makeTestToken(t, testSecret, testIssuer, testAudience, staffID, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour))
	managerToken := makeTestToken(t, testSecret, testIssuer, testAudience, managerID, RoleManager, now.Add(-time.Minute), now.Add(time.Hour))
	expiredToken := makeTestToken(t, testSecret, testIssuer, testAudience, staffID, RoleStaff, now.Add(-2*time.Hour), now.Add(-time.Hour))

	e := echo.New()
	targetHandler := func(c echo.Context) error {
		uid, ok := GetUserID(c)
		if !ok {
			return c.String(http.StatusInternalServerError, "no user id")
		}
		role, _ := GetUserRole(c)
		return c.JSON(http.StatusOK, map[string]string{
			"user_id": uid.String(),
			"role":    string(role),
		})
	}

	handler := authMid.RequireStaffOrManager()(targetHandler)

	tests := []struct {
		name       string
		authHeader string
		wantCode   int
		wantSub    string
		wantRole   Role
	}{
		{"missing header", "", http.StatusUnauthorized, "", ""},
		{"malformed header (no bearer)", "Token xyz", http.StatusUnauthorized, "", ""},
		{"expired staff token", "Bearer " + expiredToken, http.StatusUnauthorized, "", ""},
		{"customer token is forbidden", "Bearer " + customerToken, http.StatusForbidden, "", ""},
		{"staff token allowed", "Bearer " + staffToken, http.StatusOK, staffID, RoleStaff},
		{"manager token allowed", "Bearer " + managerToken, http.StatusOK, managerID, RoleManager},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/fulfillments/123/status", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler(c)
			if err != nil {
				t.Fatalf("unexpected handler error: %v", err)
			}

			if rec.Code != tc.wantCode {
				t.Fatalf("status code = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}

			if tc.wantCode == http.StatusOK {
				var res map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if res["user_id"] != tc.wantSub {
					t.Errorf("user_id = %s, want %s", res["user_id"], tc.wantSub)
				}
				if res["role"] != string(tc.wantRole) {
					t.Errorf("role = %s, want %s", res["role"], tc.wantRole)
				}
			}
		})
	}
}

func TestAuthMiddlewareAuthenticateOptional(t *testing.T) {
	v := NewVerifier(testSecret, testIssuer, testAudience)
	authMid := NewAuthMiddleware(v)
	now := time.Now().UTC()

	staffID := uuid.New().String()
	staffToken := makeTestToken(t, testSecret, testIssuer, testAudience, staffID, RoleStaff, now.Add(-time.Minute), now.Add(time.Hour))

	e := echo.New()
	targetHandler := func(c echo.Context) error {
		uid, ok := GetUserID(c)
		if ok {
			return c.String(http.StatusOK, "auth:"+uid.String())
		}
		return c.String(http.StatusOK, "anon")
	}

	optionalHandler := authMid.Authenticate(false)(targetHandler)

	t.Run("anonymous allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fulfillments", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		if err := optionalHandler(c); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || rec.Body.String() != "anon" {
			t.Fatalf("expected anon, got code %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("authenticated sets context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fulfillments", nil)
		req.Header.Set("Authorization", "Bearer "+staffToken)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		if err := optionalHandler(c); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || rec.Body.String() != "auth:"+staffID {
			t.Fatalf("expected auth, got code %d body %s", rec.Code, rec.Body.String())
		}
	})
}
