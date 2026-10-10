package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"fulfillment-service/internal/models"
	"fulfillment-service/internal/repository"
)

type fakeStore struct {
	updateErr error
	gotStatus []models.Status
}

func (f *fakeStore) List(_ context.Context, s []models.Status) ([]models.Fulfillment, error) {
	f.gotStatus = s
	return []models.Fulfillment{}, nil
}

func (f *fakeStore) GetByOrderID(context.Context, uuid.UUID) (models.Fulfillment, error) {
	return models.Fulfillment{}, repository.ErrNotFound
}

func (f *fakeStore) UpdateStatus(_ context.Context, id uuid.UUID, to models.Status) (models.Fulfillment, error) {
	if f.updateErr != nil {
		return models.Fulfillment{}, f.updateErr
	}
	return models.Fulfillment{OrderID: id, Status: to}, nil
}

func do(t *testing.T, store Store, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	h := NewFulfillmentHandler(store)
	e.GET("/fulfillments", h.List)
	e.GET("/fulfillments/:order_id", h.Get)
	e.PATCH("/fulfillments/:order_id/status", h.UpdateStatus)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("response is not an error body: %v", err)
	}
	return e.Code
}

func TestUpdateStatus(t *testing.T) {
	id := uuid.New().String()
	cases := []struct {
		name   string
		store  *fakeStore
		path   string
		body   string
		status int
		code   string
	}{
		{"ok", &fakeStore{}, "/fulfillments/" + id + "/status", `{"status":"READY_FOR_PICKUP"}`, http.StatusOK, ""},
		{"bad uuid", &fakeStore{}, "/fulfillments/nope/status", `{"status":"PICKED_UP"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"status not updatable", &fakeStore{}, "/fulfillments/" + id + "/status", `{"status":"PREPARING"}`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"not found", &fakeStore{updateErr: repository.ErrNotFound}, "/fulfillments/" + id + "/status", `{"status":"PICKED_UP"}`, http.StatusNotFound, "NOT_FOUND"},
		{"invalid transition", &fakeStore{updateErr: repository.ErrInvalidTransition}, "/fulfillments/" + id + "/status", `{"status":"PICKED_UP"}`, http.StatusConflict, "INVALID_TRANSITION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, tc.store, http.MethodPatch, tc.path, tc.body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			if tc.code != "" {
				if got := errCode(t, rec); got != tc.code {
					t.Errorf("code = %s, want %s", got, tc.code)
				}
			}
		})
	}
}

func TestListStatusFilter(t *testing.T) {
	store := &fakeStore{}
	rec := do(t, store, http.MethodGet, "/fulfillments?status=PREPARING,READY_FOR_PICKUP", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(store.gotStatus) != 2 || store.gotStatus[1] != models.StatusReady {
		t.Errorf("filter = %v", store.gotStatus)
	}

	rec = do(t, &fakeStore{}, http.MethodGet, "/fulfillments?status=BOGUS", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad filter status = %d, want 400", rec.Code)
	}
}

func TestGetNotFound(t *testing.T) {
	rec := do(t, &fakeStore{}, http.MethodGet, "/fulfillments/"+uuid.New().String(), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
