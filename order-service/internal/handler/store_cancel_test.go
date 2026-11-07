package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"order-service/internal/client"
	"order-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

type storeCancelService struct {
	service.Service
	calls   []string
	bearers []string
	err     error
}

func (s *storeCancelService) StoreCancelOrder(ctx context.Context, orderID string) error {
	s.calls = append(s.calls, orderID)
	bearer, _ := client.UserAuthorization(ctx)
	s.bearers = append(s.bearers, bearer)
	return s.err
}

const storeOrderID = "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594"

func storeCancelApp(t *testing.T, svc service.Service) *fiber.App {
	t.Helper()
	h, err := NewOrderHandler(svc, "local_payment_callback_test_token_32_bytes")
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/api/v1/orders/:id/store-cancel", h.StoreCancelOrder)
	return app
}

func storeCancelRequest(t *testing.T, app *fiber.App, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/"+storeOrderID+"/store-cancel", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Code string `json:"code"`
	}
	if resp.StatusCode != http.StatusNoContent {
		_ = json.NewDecoder(resp.Body).Decode(&body)
	}
	resp.Body.Close()
	return resp, body.Code
}

func TestStoreCancelRequiresStaffOrManagerRole(t *testing.T) {
	const staffID = "11111111-2222-3333-4444-555555555555"
	for _, tc := range []struct {
		name    string
		headers map[string]string
		status  int
		code    string
	}{
		{"no identity", map[string]string{"Authorization": "Bearer t"}, 401, "UNAUTHENTICATED"},
		{"no bearer", map[string]string{"X-User-ID": staffID, "X-User-Role": "STAFF"}, 401, "UNAUTHENTICATED"},
		{"customer", map[string]string{"X-User-ID": staffID, "X-User-Role": "CUSTOMER", "Authorization": "Bearer t"}, 403, "FORBIDDEN"},
		{"missing role", map[string]string{"X-User-ID": staffID, "Authorization": "Bearer t"}, 403, "FORBIDDEN"},
		{"lowercase role", map[string]string{"X-User-ID": staffID, "X-User-Role": "staff", "Authorization": "Bearer t"}, 403, "FORBIDDEN"},
		{"staff", map[string]string{"X-User-ID": staffID, "X-User-Role": "STAFF", "Authorization": "Bearer staff-token"}, 204, ""},
		{"manager", map[string]string{"X-User-ID": staffID, "X-User-Role": "MANAGER", "Authorization": "Bearer manager-token"}, 204, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &storeCancelService{}
			resp, code := storeCancelRequest(t, storeCancelApp(t, svc), tc.headers)
			if resp.StatusCode != tc.status || code != tc.code {
				t.Fatalf("got %d %q, want %d %q", resp.StatusCode, code, tc.status, tc.code)
			}
			if tc.status != 204 {
				if len(svc.calls) != 0 {
					t.Fatal("unauthorized caller reached the business action")
				}
				return
			}
			if len(svc.calls) != 1 || svc.calls[0] != storeOrderID || svc.bearers[0] != tc.headers["Authorization"] {
				t.Fatalf("calls = %v bearers = %v", svc.calls, svc.bearers)
			}
		})
	}
}

func TestStoreCancelMapsServiceErrors(t *testing.T) {
	svc := &storeCancelService{err: &service.AppError{Code: "ORDER_PREPARATION_STARTED", Message: "started", HTTPStatus: 409}}
	resp, code := storeCancelRequest(t, storeCancelApp(t, svc), map[string]string{"X-User-ID": "11111111-2222-3333-4444-555555555555", "X-User-Role": "STAFF", "Authorization": "Bearer t"})
	if resp.StatusCode != 409 || code != "ORDER_PREPARATION_STARTED" {
		t.Fatalf("got %d %q", resp.StatusCode, code)
	}
}
