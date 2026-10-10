package handler

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"notification-service/internal/auth"
	"notification-service/internal/hub"
)

const (
	secret   = "test-secret-that-is-at-least-32-bytes-long"
	issuer   = "gelatoflow-auth"
	audience = "gelatoflow-api"
)

func token(t *testing.T, sub uuid.UUID, role auth.Role, mutate func(*auth.Claims)) string {
	t.Helper()
	now := time.Now()
	c := &auth.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub.String(),
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
	if mutate != nil {
		mutate(c)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func newServer(t *testing.T) (*httptest.Server, *hub.Hub) {
	t.Helper()
	h := hub.New(8)
	srv := httptest.NewServer(New(auth.NewVerifier(secret, issuer, audience), h, 50*time.Millisecond).Routes())
	t.Cleanup(srv.Close)
	return srv, h
}

func open(t *testing.T, srv *httptest.Server, authorization string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/notifications/stream", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// pushWhenReady retries push until a connection receives it, which avoids
// racing a push against the handler registering its subscription.
func pushWhenReady(t *testing.T, push func() int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if push() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subscriber never registered")
}

// readFrame reads lines until a blank line and returns the non-comment fields.
func readFrame(t *testing.T, r *bufio.Reader) map[string]string {
	t.Helper()
	fields := map[string]string{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if len(fields) > 0 {
				return fields
			}
		case strings.HasPrefix(line, ":"):
			fields["comment"] = line
			return fields
		default:
			k, v, _ := strings.Cut(line, ": ")
			fields[k] = v
		}
	}
}

func TestStreamRejectsMissingOrInvalidToken(t *testing.T) {
	srv, _ := newServer(t)
	user := uuid.New()

	cases := map[string]string{
		"no header":      "",
		"not bearer":     "Basic abc",
		"garbage token":  "Bearer not-a-jwt",
		"wrong audience": "Bearer " + token(t, user, auth.RoleCustomer, func(c *auth.Claims) { c.Audience = jwt.ClaimStrings{"other"} }),
		"expired":        "Bearer " + token(t, user, auth.RoleCustomer, func(c *auth.Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }),
		"bad role":       "Bearer " + token(t, user, auth.Role("ADMIN"), nil),
		"bad subject":    "Bearer " + token(t, user, auth.RoleCustomer, func(c *auth.Claims) { c.Subject = "not-a-uuid" }),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			resp := open(t, srv, header)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != "UNAUTHORIZED" || body["message"] == "" {
				t.Errorf("error body = %v", body)
			}
		})
	}
}

func TestStreamDeliversOnlyOwnNotifications(t *testing.T) {
	srv, h := newServer(t)
	alice, bob := uuid.New(), uuid.New()

	aliceResp := open(t, srv, "Bearer "+token(t, alice, auth.RoleCustomer, nil))
	bobResp := open(t, srv, "Bearer "+token(t, bob, auth.RoleCustomer, nil))
	if aliceResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", aliceResp.StatusCode)
	}
	if ct := aliceResp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	aliceStream, bobStream := bufio.NewReader(aliceResp.Body), bufio.NewReader(bobResp.Body)
	if f := readFrame(t, aliceStream); f["comment"] != ": connected" {
		t.Fatalf("first frame = %v", f)
	}
	readFrame(t, bobStream) // ": connected"

	eventID := uuid.New()
	pushWhenReady(t, func() int {
		return h.PushToUser(alice, hub.Notification{
			ID: eventID, Type: "order.ready", Message: "Your gelato order is ready for pickup!",
			OccurredAt: time.Date(2026, 9, 2, 14, 55, 0, 0, time.UTC),
			Data:       map[string]any{"queue_number": "01"},
		})
	})

	var frame map[string]string
	for {
		frame = readFrame(t, aliceStream)
		if frame["comment"] == "" { // skip keep-alives
			break
		}
	}
	if frame["id"] != eventID.String() || frame["event"] != "order.ready" {
		t.Fatalf("frame = %v", frame)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(frame["data"]), &payload); err != nil {
		t.Fatalf("data is not JSON: %v", err)
	}
	if payload["message"] != "Your gelato order is ready for pickup!" || payload["occurred_at"] != "2026-09-02T14:55:00Z" {
		t.Errorf("payload = %v", payload)
	}

	// Bob's connection has been open the whole time and must only have seen keep-alives.
	if f := readFrame(t, bobStream); f["comment"] != ": keep-alive" {
		t.Errorf("bob received %v, want only a keep-alive", f)
	}
}

func TestStreamSendsKeepAlive(t *testing.T) {
	srv, _ := newServer(t)
	resp := open(t, srv, "Bearer "+token(t, uuid.New(), auth.RoleStaff, nil))
	reader := bufio.NewReader(resp.Body)

	readFrame(t, reader) // ": connected"
	if f := readFrame(t, reader); f["comment"] != ": keep-alive" {
		t.Errorf("expected keep-alive, got %v", f)
	}
}

func TestStreamUnsubscribesOnDisconnect(t *testing.T) {
	srv, h := newServer(t)
	user := uuid.New()
	resp := open(t, srv, "Bearer "+token(t, user, auth.RoleCustomer, nil))
	readFrame(t, bufio.NewReader(resp.Body))

	pushWhenReady(t, func() int { return h.PushToUser(user, hub.Notification{ID: uuid.New(), Type: "x"}) })
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.PushToUser(user, hub.Notification{ID: uuid.New(), Type: "x"}) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("subscriber still registered after the client disconnected")
}

func TestHealth(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Errorf("health = %d %v", resp.StatusCode, body)
	}
}
