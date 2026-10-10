package tests

import (
	invpb "batch-inventory-service/gen/inventory/v1"
	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/catalog"
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/messaging"
	"batch-inventory-service/internal/transport"
	"batch-inventory-service/migrations"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

const jwtSecret = "inventory_test_jwt_secret_not_for_production_123"
const serviceToken = "inventory_order_test_token_not_for_production_456"

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	s := os.Getenv(key)
	if s == "" {
		if os.Getenv("INTEGRATION_REQUIRED") == "1" {
			t.Fatalf("%s required", key)
		}
		t.Skip(key + " not set")
	}
	return s
}
func fixture(t *testing.T) (*inventory.Service, string) {
	t.Helper()
	raw := requireEnv(t, "TEST_DATABASE_URL")
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := "inventory_test_" + fmt.Sprintf("%x", uuid.New())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, db); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	return inventory.New(db, activeCatalog{}, time.Minute), u.String()
}

type activeCatalog struct{}

func (activeCatalog) CheckActive(context.Context, string) error { return nil }
func batch(t *testing.T, s *inventory.Service, flavor string, n int64, expiry time.Time) inventory.Batch {
	t.Helper()
	cost := int64(125)
	b, err := s.Create(context.Background(), inventory.CreateInput{FlavorID: flavor, ProductionDate: time.Now().UTC().Format("2006-01-02"), ExpiresAt: expiry, Initial: n, UnitCost: &cost, Currency: "THB"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func reserve(t *testing.T, s *inventory.Service, items ...inventory.Item) inventory.Reservation {
	t.Helper()
	r, err := s.Reserve(context.Background(), inventory.ReserveInput{OrderID: uuid.NewString(), Key: uuid.NewString(), Items: items})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func assertBalance(t *testing.T, s *inventory.Service, id string, available, reserved, sold, wasted int64) {
	t.Helper()
	b, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if b.Available != available || b.Reserved != reserved || b.Sold != sold || b.Wasted != wasted || b.Initial != b.Available+b.Reserved+b.Sold+b.Wasted {
		t.Fatalf("unexpected balance: %+v", b)
	}
}
func rpcClient(t *testing.T, s *inventory.Service) invpb.InventoryServiceClient {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := transport.NewGRPC(s, serviceToken, 2*time.Second)
	done := make(chan error, 1)
	go func() { done <- server.Serve(l) }()
	c, err := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return invpb.NewInventoryServiceClient(c)
}
func rpcCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+serviceToken)
}
func token(t *testing.T, role auth.Role) string {
	t.Helper()
	now := time.Now()
	v := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: uuid.NewString(), Issuer: "gelatoflow-auth", Audience: jwt.ClaimStrings{"gelatoflow-api"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}})
	s, err := v.SignedString([]byte(jwtSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func call(t *testing.T, h http.Handler, method, path, body, token string, expected int) []byte {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != expected {
		t.Fatalf("%s %s: want %d got %d %s", method, path, expected, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestFEFOAtomicityAndReplay(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	flavor := uuid.NewString()
	early := batch(t, s, flavor, 2, time.Now().Add(time.Hour))
	late := batch(t, s, flavor, 4, time.Now().Add(2*time.Hour))
	in := inventory.ReserveInput{OrderID: uuid.NewString(), Key: uuid.NewString(), Items: []inventory.Item{{FlavorID: flavor, Portions: 3}}}
	r, err := s.Reserve(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Allocations) != 2 || r.Allocations[0].BatchID != early.ID || r.Allocations[0].Portions != 2 || r.Allocations[1].BatchID != late.ID {
		t.Fatalf("FEFO: %+v", r)
	}
	replay, err := s.Reserve(ctx, in)
	if err != nil || replay.ID != r.ID {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	in.Items[0].Portions = 2
	if _, err = s.Reserve(ctx, in); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("key mismatch accepted", err)
	}
	in.Key = uuid.NewString()
	if _, err = s.Reserve(ctx, in); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("same order reserved twice", err)
	}
	_, err = s.Reserve(ctx, inventory.ReserveInput{OrderID: uuid.NewString(), Key: uuid.NewString(), Items: []inventory.Item{{FlavorID: flavor, Portions: 1}, {FlavorID: uuid.NewString(), Portions: 1}}})
	if !errors.Is(err, inventory.ErrStock) {
		t.Fatal(err)
	}
	assertBalance(t, s, early.ID, 0, 2, 0, 0)
	assertBalance(t, s, late.ID, 3, 1, 0, 0)
	c, err := s.Transition(ctx, true, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()})
	if err != nil || c.Status != "CONFIRMED" {
		t.Fatal(err, c)
	}
	assertBalance(t, s, early.ID, 0, 0, 2, 0)
	assertBalance(t, s, late.ID, 3, 0, 1, 0)
	_, err = s.Transition(ctx, false, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()})
	if !errors.Is(err, inventory.ErrState) {
		t.Fatal("released sold stock", err)
	}
}
func TestTwentyConcurrentReservationsAcrossTwoInstances(t *testing.T) {
	s, dsn := fixture(t)
	other, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 1, time.Now().Add(time.Hour))
	clients := []invpb.InventoryServiceClient{rpcClient(t, s), rpcClient(t, inventory.New(other, activeCatalog{}, time.Minute))}
	start := make(chan struct{})
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+serviceToken)
			_, err := clients[i%2].ReservePortions(ctx, &invpb.ReservePortionsRequest{OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString(), Items: []*invpb.PortionRequest{{FlavorId: flavor, Portions: 1}}})
			results <- err
		}(i)
	}
	close(start)
	success := 0
	for i := 0; i < 20; i++ {
		err := <-results
		if err == nil {
			success++
		} else if status.Code(err) != codes.FailedPrecondition {
			t.Error(err)
		}
	}
	if success != 1 {
		t.Fatalf("successes=%d", success)
	}
	assertBalance(t, s, b.ID, 0, 1, 0, 0)
}
func TestConcurrentIdempotencyAndTerminalRaces(t *testing.T) {
	s, _ := fixture(t)
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 5, time.Now().Add(time.Hour))
	in := inventory.ReserveInput{OrderID: uuid.NewString(), Key: uuid.NewString(), Items: []inventory.Item{{FlavorID: flavor, Portions: 2}}}
	type result struct {
		r inventory.Reservation
		e error
	}
	results := make(chan result, 10)
	for i := 0; i < 10; i++ {
		go func() { r, e := s.Reserve(context.Background(), in); results <- result{r, e} }()
	}
	id := ""
	for i := 0; i < 10; i++ {
		v := <-results
		if v.e != nil {
			t.Fatal(v.e)
		}
		if id != "" && id != v.r.ID {
			t.Fatal("duplicate reservations")
		}
		id = v.r.ID
	}
	assertBalance(t, s, b.ID, 3, 2, 0, 0)
	term := make(chan error, 2)
	for _, confirm := range []bool{true, false} {
		go func(confirm bool) {
			_, err := s.Transition(context.Background(), confirm, inventory.TransitionInput{ReservationID: id, OrderID: in.OrderID, Key: uuid.NewString()})
			term <- err
		}(confirm)
	}
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-term
		if err == nil {
			successes++
		} else if !errors.Is(err, inventory.ErrState) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("terminal successes %d", successes)
	}
	got, err := s.Get(context.Background(), b.ID)
	if err != nil || got.Reserved != 0 || got.Initial != got.Available+got.Sold+got.Wasted {
		t.Fatal(err, got)
	}
}
func TestExpiryAndWasteOutbox(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	flavor := uuid.NewString()
	b := batch(t, s, flavor, 5, time.Now().Add(time.Hour))
	r := reserve(t, s, inventory.Item{FlavorID: flavor, Portions: 2})
	_, err := s.DB.Exec(ctx, "UPDATE reservations SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(ctx, true, inventory.TransitionInput{ReservationID: r.ID, OrderID: r.OrderID, Key: uuid.NewString()}); !errors.Is(err, inventory.ErrState) {
		t.Fatal("late confirmation accepted", err)
	}
	if err = s.Sweep(ctx, 100); err != nil {
		t.Fatal(err)
	}
	assertBalance(t, s, b.ID, 5, 0, 0, 0)
	r = reserve(t, s, inventory.Item{FlavorID: flavor, Portions: 2})
	if _, err = s.DB.Exec(ctx, "UPDATE batches SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE reservations SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Availability(ctx, flavor)
	if err != nil || len(a) != 1 || a[0].Available != 0 {
		t.Fatal("expired availability", err, a)
	}
	for i := 0; i < 2; i++ {
		if err = s.Sweep(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	assertBalance(t, s, b.ID, 0, 0, 0, 5)
	var count, total int64
	if err = s.DB.QueryRow(ctx, "SELECT count(*),sum(portions) FROM waste_records").Scan(&count, &total); err != nil || count != 2 || total != 5 {
		t.Fatal("duplicate expiry", count, total, err)
	}
	var events int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&events); err != nil || events != 2 {
		t.Fatal(events, err)
	}
	var payload []byte
	if err = s.DB.QueryRow(ctx, "SELECT payload FROM outbox_events LIMIT 1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var e map[string]any
	json.Unmarshal(payload, &e)
	if e["specversion"] != "1.0" || e["type"] != "com.gelatoflow.inventory.waste-recorded.v1" || e["data"].(map[string]any)["currency"] != "THB" {
		t.Fatalf("event %s", payload)
	}
}
func TestWasteReplayAndReservedProtection(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	f := uuid.NewString()
	b := batch(t, s, f, 4, time.Now().Add(time.Hour))
	reserve(t, s, inventory.Item{FlavorID: f, Portions: 3})
	actor := uuid.NewString()
	in := inventory.WasteInput{Portions: 2, Reason: "DAMAGED", Key: uuid.NewString()}
	if _, err := s.Waste(ctx, b.ID, actor, in); !errors.Is(err, inventory.ErrStock) {
		t.Fatal("wasted reserved portions", err)
	}
	in.Portions = 1
	w, err := s.Waste(ctx, b.ID, actor, in)
	if err != nil || w.CostLost != 125 {
		t.Fatal(err, w)
	}
	again, err := s.Waste(ctx, b.ID, actor, in)
	if err != nil || again.ID != w.ID {
		t.Fatal(err, again)
	}
	assertBalance(t, s, b.ID, 0, 3, 0, 1)
	in.Reason = "QUALITY"
	if _, err = s.Waste(ctx, b.ID, actor, in); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
}
func TestHTTPAndGRPCContracts(t *testing.T) {
	s, _ := fixture(t)
	h := transport.NewHTTP(s, auth.NewVerifier(jwtSecret, "gelatoflow-auth", "gelatoflow-api"), time.Second)
	manager := token(t, auth.RoleManager)
	staff := token(t, auth.RoleStaff)
	customer := token(t, auth.RoleCustomer)
	flavor := uuid.NewString()
	input := fmt.Sprintf(`{"flavor_id":%q,"production_date":%q,"expires_at":%q,"initial_portions":4,"unit_cost_minor":0,"currency":"THB"}`, flavor, time.Now().UTC().Format("2006-01-02"), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	call(t, h, "POST", "/api/v1/inventory/batches", input, "", 401)
	call(t, h, "POST", "/api/v1/inventory/batches", input, staff, 403)
	call(t, h, "POST", "/api/v1/inventory/batches", input, customer, 403)
	for _, bad := range []string{input + " {}", `{"initial_portions":1,"initial_portions":2}`, `{"CURRENCY":"THB"}`, `{"currency":null}`, `{"unknown":1}`, `null`} {
		call(t, h, "POST", "/api/v1/inventory/batches", bad, manager, 400)
	}
	var b inventory.Batch
	json.Unmarshal(call(t, h, "POST", "/api/v1/inventory/batches", input, manager, 201), &b)
	call(t, h, "GET", "/api/v1/inventory/batches", "", staff, 200)
	call(t, h, "GET", "/api/v1/inventory/batches/"+b.ID, "", customer, 403)
	call(t, h, "GET", "/api/v1/inventory/availability?flavor_id="+flavor, "", "bad-token", 401)
	call(t, h, "GET", "/api/v1/inventory/availability?flavor_id="+flavor+"&flavor_id="+flavor, "", "", 400)
	c := rpcClient(t, s)
	req := &invpb.ReservePortionsRequest{OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString(), Items: []*invpb.PortionRequest{{FlavorId: flavor, Portions: 2}}}
	if _, err := c.ReservePortions(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	bad := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+manager)
	if _, err := c.ReservePortions(bad, req); status.Code(err) != codes.Unauthenticated {
		t.Fatal("user JWT accepted as Order identity", err)
	}
	dup := metadata.AppendToOutgoingContext(rpcCtx(t), "authorization", "Bearer "+serviceToken)
	if _, err := c.ReservePortions(dup, req); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	check, err := c.CheckAvailability(rpcCtx(t), &invpb.CheckAvailabilityRequest{Items: req.Items})
	if err != nil || !check.AllAvailable || check.Items[0].AvailablePortions != 4 {
		t.Fatal(err, check)
	}
	r, err := c.ReservePortions(rpcCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	var rest inventory.Batch
	json.Unmarshal(call(t, h, "GET", "/api/v1/inventory/batches/"+b.ID, "", staff, 200), &rest)
	if rest.Available != 2 || rest.Reserved != 2 {
		t.Fatal(rest)
	}
	_, err = c.ConfirmReservation(rpcCtx(t), &invpb.ConfirmReservationRequest{ReservationId: r.Reservation.ReservationId, OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString()})
	if status.Code(err) != codes.NotFound {
		t.Fatal("wrong order accepted", err)
	}
	released, err := c.ReleaseReservation(rpcCtx(t), &invpb.ReleaseReservationRequest{ReservationId: r.Reservation.ReservationId, OrderId: req.OrderId, IdempotencyKey: uuid.NewString(), Reason: "cancelled"})
	if err != nil || released.Reservation.Status != invpb.ReservationStatus_RESERVATION_STATUS_RELEASED {
		t.Fatal(err, released)
	}
	assertBalance(t, s, b.ID, 4, 0, 0, 0)
}

func TestOutboxRetryAndRealRabbit(t *testing.T) {
	raw := requireEnv(t, "TEST_RABBITMQ_URL")
	s, _ := fixture(t)
	ctx := context.Background()
	b := batch(t, s, uuid.NewString(), 1, time.Now().Add(time.Hour))
	if _, err := s.Waste(ctx, b.ID, uuid.NewString(), inventory.WasteInput{Portions: 1, Reason: "DAMAGED", Key: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	downURL, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	downURL.Host = address(t)
	if _, err := messaging.DispatchOne(ctx, s.DB, messaging.Rabbit{URL: downURL.String()}); err == nil {
		t.Fatal("expected broker failure")
	}
	var id string
	var published *time.Time
	var attempts int
	if err := s.DB.QueryRow(ctx, "SELECT id::text,published_at,attempts FROM outbox_events").Scan(&id, &published, &attempts); err != nil || published != nil || attempts != 1 {
		t.Fatal(err, published, attempts)
	}
	conn, err := amqp.Dial(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	if err = ch.ExchangeDeclare("inventory", "topic", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = ch.QueueBind(q.Name, "inventory.waste", "inventory", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, "UPDATE outbox_events SET next_attempt_at=clock_timestamp()"); err != nil {
		t.Fatal(err)
	}
	if _, err = messaging.DispatchOne(ctx, s.DB, messaging.Rabbit{URL: raw}); err != nil {
		t.Fatal(err)
	}
	msg, ok, err := ch.Get(q.Name, true)
	if err != nil || !ok || msg.MessageId != id || msg.RoutingKey != "inventory.waste" {
		t.Fatal(err, ok, msg.MessageId)
	}
	if err = s.DB.QueryRow(ctx, "SELECT published_at FROM outbox_events").Scan(&published); err != nil || published == nil {
		t.Fatal(err, published)
	}
	// Simulate lost DB acknowledgement after broker delivery: the same event ID is
	// republished, never a new business event. Consumers must deduplicate this ID.
	if _, err = s.DB.Exec(ctx, "UPDATE outbox_events SET published_at=NULL,next_attempt_at=clock_timestamp()"); err != nil {
		t.Fatal(err)
	}
	if _, err = messaging.DispatchOne(ctx, s.DB, messaging.Rabbit{URL: raw}); err != nil {
		t.Fatal(err)
	}
	retry, ok, err := ch.Get(q.Name, true)
	if err != nil || !ok || retry.MessageId != id || !bytes.Equal(retry.Body, msg.Body) {
		t.Fatal("replay lost identity", err)
	}
	if err = ch.QueueUnbind(q.Name, "inventory.waste", "inventory", nil); err != nil {
		t.Fatal(err)
	}
	if err = (messaging.Rabbit{URL: raw}).Publish(ctx, uuid.NewString(), []byte(`{}`)); err == nil {
		t.Fatal("unroutable publish accepted")
	}
}
func TestCatalogClientAgainstRealCatalog(t *testing.T) {
	addr := requireEnv(t, "TEST_CATALOG_GRPC_ADDR")
	rest := requireEnv(t, "TEST_CATALOG_REST_URL")
	s, _ := fixture(t)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s.Catalog = catalog.New(conn, time.Second)
	body := fmt.Sprintf(`{"name":%q,"description":"Inventory integration","price":{"amount_minor":6000,"currency":"THB"},"allergens":[],"recipe":"test"}`, "Inventory "+uuid.NewString())
	manager := token(t, auth.RoleManager)
	request, err := http.NewRequest("POST", rest+"/api/v1/flavors", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+manager)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("Catalog create: %d %s", resp.StatusCode, raw)
	}
	var flavor struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(raw, &flavor); err != nil {
		t.Fatal(err)
	}
	archive := func() {
		req, _ := http.NewRequest("DELETE", rest+"/api/v1/flavors/"+flavor.ID, nil)
		req.Header.Set("Authorization", "Bearer "+manager)
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Error(resp.Status)
		}
	}
	defer archive()
	batch(t, s, flavor.ID, 2, time.Now().Add(time.Hour))
	archive()
	if err = s.Catalog.CheckActive(context.Background(), flavor.ID); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal("inactive flavor accepted", err)
	}
	if err = s.Catalog.CheckActive(context.Background(), uuid.NewString()); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal("missing flavor accepted", err)
	}
}
func TestConcurrentSweep(t *testing.T) {
	s, _ := fixture(t)
	b := batch(t, s, uuid.NewString(), 6, time.Now().Add(time.Hour))
	r := reserve(t, s, inventory.Item{FlavorID: b.FlavorID, Portions: 2})
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, "UPDATE batches SET expires_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, "UPDATE reservations SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Sweep(ctx, 100) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertBalance(t, s, b.ID, 0, 0, 0, 6)
}
