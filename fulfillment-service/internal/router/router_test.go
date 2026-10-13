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
}

func (m *mockStore) List(_ context.Context, _ []models.Status) ([]models.Fulfillment, error) {
	return []models.Fulfillment{}, nil
}

func (m *mockStore) GetByOrderID(_ context.Context, id uuid.UUID) (models.Fulfillment, error) {
	return models.Fulfillment{OrderID: id, Status: models.StatusPreparing}, nil
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

func TestRouterWithoutVerifier(t *testing.T) {
	store := &mockStore{}
	h := handler.NewFulfillmentHandler(store)
	e := New(h)

	orderID := uuid.New().String()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/fulfillments/"+orderID+"/status", strings.NewReader(`{"status":"READY_FOR_PICKUP"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
