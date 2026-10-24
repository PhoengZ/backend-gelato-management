package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"fulfillment-service/internal/middleware"
	"fulfillment-service/internal/models"
	"fulfillment-service/internal/repository"
)

type fakeStore struct {
	updateErr     error
	gotStatus     []models.Status
	gotFilter     models.ListFilter
	capacityCount int
	capacityErr   error
	gotSlotTime   time.Time
	gotActorID    *uuid.UUID
	ticket        *models.Fulfillment // nil means not found
	history       []models.FulfillmentStatusHistory
	gotHistoryFor uuid.UUID
}

func (f *fakeStore) List(_ context.Context, filter models.ListFilter) ([]models.Fulfillment, error) {
	f.gotFilter = filter
	f.gotStatus = filter.Statuses
	return []models.Fulfillment{}, nil
}

func (f *fakeStore) GetByOrderID(context.Context, uuid.UUID) (models.Fulfillment, error) {
	if f.ticket == nil {
		return models.Fulfillment{}, repository.ErrNotFound
	}
	return *f.ticket, nil
}

func (f *fakeStore) GetHistory(_ context.Context, id uuid.UUID) ([]models.FulfillmentStatusHistory, error) {
	f.gotHistoryFor = id
	return f.history, nil
}

func (f *fakeStore) UpdateStatus(ctx context.Context, id uuid.UUID, to models.Status) (models.Fulfillment, error) {
	f.gotActorID = repository.ActorIDFromContext(ctx)
	if f.updateErr != nil {
		return models.Fulfillment{}, f.updateErr
	}
	return models.Fulfillment{OrderID: id, Status: to}, nil
}

func (f *fakeStore) GetSlotCapacity(_ context.Context, slotTime time.Time) (int, error) {
	f.gotSlotTime = slotTime
	if f.capacityErr != nil {
		return 0, f.capacityErr
	}
	return f.capacityCount, nil
}

func do(t *testing.T, store Store, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	h := NewFulfillmentHandler(store)
	e.GET("/fulfillments", h.List)
	e.GET("/fulfillments/slots/capacity", h.GetSlotCapacity)
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

func TestListPickupWindow(t *testing.T) {
	store := &fakeStore{}
	rec := do(t, store, http.MethodGet, "/fulfillments?pickup_from=2026-10-13T00:00:00Z&pickup_to=2026-10-14T00:00:00Z", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	from, to := store.gotFilter.PickupFrom, store.gotFilter.PickupTo
	if from == nil || to == nil || from.Format(time.RFC3339) != "2026-10-13T00:00:00Z" || to.Format(time.RFC3339) != "2026-10-14T00:00:00Z" {
		t.Fatalf("filter = %+v", store.gotFilter)
	}

	for name, query := range map[string]string{
		"bad format":     "pickup_from=yesterday",
		"empty window":   "pickup_from=2026-10-14T00:00:00Z&pickup_to=2026-10-14T00:00:00Z",
		"reverse window": "pickup_from=2026-10-14T00:00:00Z&pickup_to=2026-10-13T00:00:00Z",
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, &fakeStore{}, http.MethodGet, "/fulfillments?"+query, "")
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "VALIDATION_ERROR" {
				t.Fatalf("status = %d (%s), want 400 VALIDATION_ERROR", rec.Code, rec.Body.String())
			}
		})
	}
}

// getAs calls Get with an authenticated identity already in the context, as the
// auth middleware would leave it.
func getAs(t *testing.T, store Store, role middleware.Role, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	id := uuid.New().String()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/fulfillments/"+id, nil), rec)
	c.SetParamNames("order_id")
	c.SetParamValues(id)
	c.Set(middleware.ContextUserID, userID.String())
	c.Set(middleware.ContextUserRole, role)
	if err := NewFulfillmentHandler(store).Get(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestGetCustomerOwnership(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	owned := &fakeStore{ticket: &models.Fulfillment{OrderID: uuid.New(), CustomerID: &owner, QueueNumber: "01"}}
	legacy := &fakeStore{ticket: &models.Fulfillment{OrderID: uuid.New(), QueueNumber: "02"}}

	for _, tc := range []struct {
		name  string
		store *fakeStore
		role  middleware.Role
		user  uuid.UUID
		want  int
	}{
		{"owner sees ticket", owned, middleware.RoleCustomer, owner, http.StatusOK},
		{"other customer gets 404", owned, middleware.RoleCustomer, stranger, http.StatusNotFound},
		{"ticket without customer_id is hidden from customers", legacy, middleware.RoleCustomer, owner, http.StatusNotFound},
		{"staff sees any ticket", legacy, middleware.RoleStaff, stranger, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := getAs(t, tc.store, tc.role, tc.user)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "customer_id") {
				t.Error("customer_id must not appear in the response")
			}
		})
	}
}

func TestHistory(t *testing.T) {
	ticketID := uuid.New()
	store := &fakeStore{
		ticket:  &models.Fulfillment{ID: ticketID},
		history: []models.FulfillmentStatusHistory{{FulfillmentID: ticketID, NewStatus: models.StatusPreparing}},
	}
	e := echo.New()
	h := NewFulfillmentHandler(store)
	e.GET("/fulfillments/:order_id/history", h.History)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fulfillments/"+uuid.New().String()+"/history", nil))
	if rec.Code != http.StatusOK || store.gotHistoryFor != ticketID {
		t.Fatalf("status = %d, history for %s (%s)", rec.Code, store.gotHistoryFor, rec.Body.String())
	}
	var body struct {
		Items []models.FulfillmentStatusHistory `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 {
		t.Fatalf("body = %s", rec.Body.String())
	}

	store.ticket = nil
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fulfillments/"+uuid.New().String()+"/history", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing order status = %d, want 404", rec.Code)
	}
}

func TestHealthDatabaseCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		ping func(context.Context) error
		want int
	}{
		"database up":   {func(context.Context) error { return nil }, http.StatusOK},
		"database down": {func(context.Context) error { return errors.New("refused") }, http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			h := NewFulfillmentHandler(&fakeStore{}).WithDBCheck(tc.ping)
			e := echo.New()
			e.GET("/health", h.Health)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestGetNotFound(t *testing.T) {
	rec := do(t, &fakeStore{}, http.MethodGet, "/fulfillments/"+uuid.New().String(), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestGetSlotCapacity(t *testing.T) {
	t.Run("missing slot_time parameter", func(t *testing.T) {
		rec := do(t, &fakeStore{}, http.MethodGet, "/fulfillments/slots/capacity", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if got := errCode(t, rec); got != "VALIDATION_ERROR" {
			t.Errorf("code = %s, want VALIDATION_ERROR", got)
		}
	})

	t.Run("invalid slot_time format", func(t *testing.T) {
		rec := do(t, &fakeStore{}, http.MethodGet, "/fulfillments/slots/capacity?slot_time=not-a-timestamp", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if got := errCode(t, rec); got != "VALIDATION_ERROR" {
			t.Errorf("code = %s, want VALIDATION_ERROR", got)
		}
	})

	t.Run("valid slot_time returns active capacity", func(t *testing.T) {
		store := &fakeStore{capacityCount: 4}
		slotStr := "2026-10-13T14:05:00Z"
		rec := do(t, store, http.MethodGet, "/fulfillments/slots/capacity?slot_time="+slotStr, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}

		var resp models.SlotCapacityResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if resp.ActiveTicketCount != 4 || resp.ActiveCount != 4 {
			t.Errorf("active tickets = %d/%d, want 4", resp.ActiveTicketCount, resp.ActiveCount)
		}
		expectedStart, _ := time.Parse(time.RFC3339, "2026-10-13T14:00:00Z")
		expectedEnd, _ := time.Parse(time.RFC3339, "2026-10-13T14:15:00Z")
		if !resp.SlotStart.Equal(expectedStart) {
			t.Errorf("slot_start = %v, want %v", resp.SlotStart, expectedStart)
		}
		if !resp.SlotEnd.Equal(expectedEnd) {
			t.Errorf("slot_end = %v, want %v", resp.SlotEnd, expectedEnd)
		}
	})

	t.Run("store failure returns 500", func(t *testing.T) {
		store := &fakeStore{capacityErr: context.DeadlineExceeded}
		rec := do(t, store, http.MethodGet, "/fulfillments/slots/capacity?slot_time=2026-10-13T14:00:00Z", "")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := errCode(t, rec); got != "INTERNAL_ERROR" {
			t.Errorf("code = %s, want INTERNAL_ERROR", got)
		}
	})
}

func TestUpdateStatusActorPropagation(t *testing.T) {
	store := &fakeStore{}
	h := NewFulfillmentHandler(store)
	e := echo.New()

	actorID := uuid.New()
	id := uuid.New().String()

	req := httptest.NewRequest(http.MethodPatch, "/fulfillments/"+id+"/status", strings.NewReader(`{"status":"READY_FOR_PICKUP"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("order_id")
	c.SetParamValues(id)
	c.Set(middleware.ContextUserID, actorID.String())

	if err := h.UpdateStatus(c); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if store.gotActorID == nil || *store.gotActorID != actorID {
		t.Fatalf("actorID = %v, want %s", store.gotActorID, actorID)
	}
}
