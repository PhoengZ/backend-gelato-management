package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const ticketOrderID = "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594"

func TestGetTicketForwardsCallerBearerAndReadsStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/fulfillments/"+ticketOrderID {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer staff-token" {
			t.Errorf("Authorization = %q, want the caller bearer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"order_id":"` + ticketOrderID + `","status":"QUEUED","queue_number":7}`))
	}))
	defer server.Close()
	cli, err := NewFulfillmentClient(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := cli.GetTicket(WithUserAuthorization(context.Background(), "Bearer staff-token"), ticketOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Status != "QUEUED" || ticket.QueueNumber != 7 {
		t.Fatalf("ticket = %+v", ticket)
	}
}

func TestGetTicketDistinguishesMissingTicketFromOutage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		missing bool
	}{
		{"not found", http.StatusNotFound, `{"code":"NOT_FOUND"}`, true},
		{"server error", http.StatusInternalServerError, `{}`, false},
		{"bad gateway", http.StatusBadGateway, `{}`, false},
		{"malformed", http.StatusOK, `not json`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			cli, _ := NewFulfillmentClient(server.URL, time.Second)
			_, err := cli.GetTicket(context.Background(), ticketOrderID)
			if err == nil {
				t.Fatal("expected error")
			}
			if errors.Is(err, ErrTicketNotFound) != tc.missing {
				t.Fatalf("ErrTicketNotFound = %v, want %v (%v)", errors.Is(err, ErrTicketNotFound), tc.missing, err)
			}
		})
	}
}

// Fulfillment re-verifies the forwarded JWT; its refusal is not an outage.
func TestGetTicketReportsRejectedCredentials(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		cli, _ := NewFulfillmentClient(server.URL, time.Second)
		_, err := cli.GetTicket(context.Background(), ticketOrderID)
		server.Close()
		if !errors.Is(err, ErrTicketForbidden) {
			t.Fatalf("%d: err = %v, want ErrTicketForbidden", code, err)
		}
	}
}

func TestGetTicketRejectsInvalidOrderID(t *testing.T) {
	cli, _ := NewFulfillmentClient("http://127.0.0.1:1", time.Second)
	if _, err := cli.GetTicket(context.Background(), "../slots/capacity"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
