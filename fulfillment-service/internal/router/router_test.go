package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"fulfillment-service/internal/handler"
	"fulfillment-service/internal/middleware"
	"fulfillment-service/internal/models"
)

type mockStore struct {
	capacityCount int
	gotStatus     models.Status
	customerID    *uuid.UUID
}

func (m *mockStore) List(_ context.Context, _ models.ListFilter) ([]models.Fulfillment, error) {
	return []models.Fulfillment{}, nil
}

func (m *mockStore) GetByOrderID(_ context.Context, id uuid.UUID) (models.Fulfillment, error) {
	return models.Fulfillment{OrderID: id, CustomerID: m.customerID, Status: models.StatusPreparing}, nil
}

func (m *mockStore) GetHistory(context.Context, uuid.UUID) ([]models.FulfillmentStatusHistory, error) {
	return []models.FulfillmentStatusHistory{}, nil
}

func (m *mockStore) UpdateStatus(_ context.Context, id uuid.UUID, to models.Status) (models.Fulfillment, error) {
	m.gotStatus = to
	return models.Fulfillment{OrderID: id, Status: to}, nil
}

func (m *mockStore) GetSlotCapacity(_ context.Context, _ time.Time) (int, error) {
	return m.capacityCount, nil
}

func makeToken(t *testing.T, secret, issuer, audience, sub string, role middleware.Role) string {
	t.Helper()
	claims := middleware.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC().Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}

func TestRouterWithAuth(t *testing.T) {
	secret := "secret-key-for-router-test-at-least-32-bytes"
	issuer := "gelatoflow-auth"
	audience := "gelatoflow-api"

	verifier := middleware.NewVerifier(secret, issuer, audience)
	store := &mockStore{capacityCount: 7}
	h := handler.NewFulfillmentHandler(store)
	e := New(h, verifier)

	t.Run("health check works", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("health status = %d, want 200", rec.Code)
		}
	})

	t.Run("slot capacity endpoint is accessible without staff token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fulfillments/slots/capacity?slot_time=2026-10-13T14:00:00Z", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	})

	orderID := uuid.New().String()
	patchPath := "/api/v1/fulfillments/" + orderID + "/status"
	patchBody := `{"status":"READY_FOR_PICKUP"}`

	t.Run("patch status without auth is rejected 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, patchPath, strings.NewReader(patchBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("patch status with customer token is rejected 403", func(t *testing.T) {
		custToken := makeToken(t, secret, issuer, audience, uuid.New().String(), middleware.RoleCustomer)
		req := httptest.NewRequest(http.MethodPatch, patchPath, strings.NewReader(patchBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+custToken)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("patch status with staff token is allowed 200", func(t *testing.T) {
		staffToken := makeToken(t, secret, issuer, audience, uuid.New().String(), middleware.RoleStaff)
		req := httptest.NewRequest(http.MethodPatch, patchPath, strings.NewReader(patchBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+staffToken)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if store.gotStatus != models.StatusReady {
			t.Fatalf("status = %v, want READY_FOR_PICKUP", store.gotStatus)
		}
	})

	t.Run("patch status with manager token is allowed 200", func(t *testing.T) {
		mgrToken := makeToken(t, secret, issuer, audience, uuid.New().String(), middleware.RoleManager)
		req := httptest.NewRequest(http.MethodPatch, patchPath, strings.NewReader(`{"status":"PICKED_UP"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+mgrToken)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if store.gotStatus != models.StatusPickedUp {
			t.Fatalf("status = %v, want PICKED_UP", store.gotStatus)
		}
	})
}

func TestRouterRoleAccess(t *testing.T) {
	secret := "secret-key-for-router-test-at-least-32-bytes"
	issuer, audience := "gelatoflow-auth", "gelatoflow-api"
	customerID := uuid.New()
	e := New(handler.NewFulfillmentHandler(&mockStore{customerID: &customerID}), middleware.NewVerifier(secret, issuer, audience))

	customer := makeToken(t, secret, issuer, audience, customerID.String(), middleware.RoleCustomer)
	staff := makeToken(t, secret, issuer, audience, uuid.New().String(), middleware.RoleStaff)
	manager := makeToken(t, secret, issuer, audience, uuid.New().String(), middleware.RoleManager)
	orderPath := "/api/v1/fulfillments/" + uuid.New().String()

	for _, tc := range []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"customer reads own ticket", http.MethodGet, orderPath, customer, "", http.StatusOK},
		{"customer cannot list the queue", http.MethodGet, "/api/v1/fulfillments", customer, "", http.StatusForbidden},
		{"customer cannot change status", http.MethodPatch, orderPath + "/status", customer, `{"status":"READY_FOR_PICKUP"}`, http.StatusForbidden},
		{"staff cannot read history", http.MethodGet, orderPath + "/history", staff, "", http.StatusForbidden},
		{"manager reads history", http.MethodGet, orderPath + "/history", manager, "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestRouterSetsRequestID(t *testing.T) {
	e := New(handler.NewFulfillmentHandler(&mockStore{}),
		middleware.NewVerifier("secret-key-for-router-test-at-least-32-bytes", "gelatoflow-auth", "gelatoflow-api"))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("response has no X-Request-ID")
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", "from-gateway")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "from-gateway" {
		t.Fatalf("X-Request-ID = %q, want the incoming id", got)
	}
}

func TestRouterRequiresVerifier(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New without a verifier must panic instead of serving unauthenticated routes")
		}
	}()
	New(handler.NewFulfillmentHandler(&mockStore{}), nil)
}

func TestHealthReportsBrokerStatus(t *testing.T) {
	verifier := middleware.NewVerifier("secret-key-for-router-test-at-least-32-bytes", "gelatoflow-auth", "gelatoflow-api")

	for name, tc := range map[string]struct {
		brokerUp func() bool
		want     int
	}{
		"no broker configured": {nil, http.StatusOK},
		"broker connected":     {func() bool { return true }, http.StatusOK},
		"broker disconnected":  {func() bool { return false }, http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			h := handler.NewFulfillmentHandler(&mockStore{})
			if tc.brokerUp != nil {
				h.WithBrokerStatus(tc.brokerUp)
			}
			rec := httptest.NewRecorder()
			New(h, verifier).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
			if rec.Code != tc.want {
				t.Fatalf("health status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
