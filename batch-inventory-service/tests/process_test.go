package tests

import (
	pb "batch-inventory-service/gen/inventory/v1"
	"batch-inventory-service/internal/inventory"
	"context"
	"fmt"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func address(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func startProcess(t *testing.T, bin, dsn string) (pb.InventoryServiceClient, func()) {
	t.Helper()
	httpAddr, grpcAddr := address(t), address(t)
	logfile, err := os.CreateTemp(t.TempDir(), "inventory-process")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logfile.Close() })
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn, "HTTP_ADDR="+httpAddr, "GRPC_ADDR="+grpcAddr, "CATALOG_GRPC_ADDR=127.0.0.1:50052", "JWT_SECRET="+jwtSecret, "ORDER_SERVICE_TOKEN="+serviceToken, "JWT_ISSUER=gelatoflow-auth", "JWT_AUDIENCE=gelatoflow-api", "PUBLISH_EVENTS=false", "SWEEP_INTERVAL=100ms", "REQUEST_TIMEOUT=2s", "RESERVATION_TTL=1m")
	cmd.Stdout = logfile
	cmd.Stderr = logfile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
				if waitErr != nil {
					data, _ := os.ReadFile(logfile.Name())
					t.Errorf("process exit %v: %s", waitErr, data)
				}
			case <-time.After(7 * time.Second):
				cmd.Process.Kill()
				<-done
				t.Error("shutdown exceeded bound")
			}
		})
	}
	t.Cleanup(stop)
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case <-done:
			data, _ := os.ReadFile(logfile.Name())
			t.Fatalf("startup exit %v: %s", waitErr, data)
		default:
		}
		resp, err := client.Get("http://" + httpAddr + "/ready")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("process not ready")
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return pb.NewInventoryServiceClient(conn), stop
}
func TestTwoProcessesAndRestartPreserveReservation(t *testing.T) {
	s, dsn := fixture(t)
	bin := filepath.Join(t.TempDir(), "inventory")
	build := exec.Command("go", "build", "-race", "-o", bin, "../cmd/api")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	b := batch(t, s, uuid.NewString(), 1, time.Now().Add(time.Hour))
	a, stopA := startProcess(t, bin, dsn)
	c, stopB := startProcess(t, bin, dsn)
	order, key := uuid.NewString(), uuid.NewString()
	req := &pb.ReservePortionsRequest{OrderId: order, IdempotencyKey: key, Items: []*pb.PortionRequest{{FlavorId: b.FlavorID, Portions: 1}}}
	r, err := a.ReservePortions(rpcCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := c.ReservePortions(rpcCtx(t), req)
	if err != nil || replay.Reservation.ReservationId != r.Reservation.ReservationId {
		t.Fatal("cross-process replay", err)
	}
	stopA()
	stopB()
	restarted, stop := startProcess(t, bin, dsn)
	defer stop()
	replay, err = restarted.ReservePortions(rpcCtx(t), req)
	if err != nil || replay.Reservation.ReservationId != r.Reservation.ReservationId {
		t.Fatal("restart lost idempotency", err)
	}
	confirmed, err := restarted.ConfirmReservation(rpcCtx(t), &pb.ConfirmReservationRequest{OrderId: order, ReservationId: r.Reservation.ReservationId, IdempotencyKey: uuid.NewString()})
	if err != nil || confirmed.Reservation.Status != pb.ReservationStatus_RESERVATION_STATUS_CONFIRMED {
		t.Fatal(err, confirmed)
	}
	assertBalance(t, s, b.ID, 0, 0, 1, 0)
}
func TestGRPCDeadlineRollsBackBlockedReservation(t *testing.T) {
	s, _ := fixture(t)
	b := batch(t, s, uuid.NewString(), 1, time.Now().Add(time.Hour))
	tx, err := s.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), "SELECT id FROM batches WHERE id=$1 FOR UPDATE", b.ID); err != nil {
		t.Fatal(err)
	}
	c := rpcClient(t, s)
	ctx, cancel := context.WithTimeout(rpcCtx(t), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.ReservePortions(ctx, &pb.ReservePortionsRequest{OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString(), Items: []*pb.PortionRequest{{FlavorId: b.FlavorID, Portions: 1}}})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline ignored")
	}
	tx.Rollback(context.Background())
	// Wait for rollback to finish by acquiring the same row in a fresh transaction.
	check, err := s.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer check.Rollback(context.Background())
	if _, err = check.Exec(context.Background(), "SELECT id FROM batches WHERE id=$1 FOR UPDATE", b.ID); err != nil {
		t.Fatal(err)
	}
	assertBalance(t, s, b.ID, 1, 0, 0, 0)
	var n int
	if err = check.QueryRow(context.Background(), "SELECT count(*) FROM reservations").Scan(&n); err != nil || n != 0 {
		t.Fatal(fmt.Sprintf("reservation persisted after timeout: %d %v", n, err))
	}
}
func TestExpiryTTLAndInvalidRequests(t *testing.T) {
	s, _ := fixture(t)
	s.TTL = 2 * time.Hour
	// Exercise sub-microsecond input even on hosts with a coarser clock.
	expiry := time.Now().Add(time.Hour).Truncate(time.Microsecond).Add(867 * time.Nanosecond)
	b := batch(t, s, uuid.NewString(), 2, expiry)
	r := reserve(t, s, inventory.Item{FlavorID: b.FlavorID, Portions: 1})
	// PostgreSQL persists timestamps at microsecond precision. The reservation
	// must use the stored expiry, rather than the original nanosecond input.
	stored, err := s.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.ExpiresAt.Equal(stored.ExpiresAt) || r.ExpiresAt.After(expiry) {
		t.Fatalf("TTL not capped at persisted batch expiry: %s vs %s (input %s)", r.ExpiresAt, stored.ExpiresAt, expiry)
	}
	c := rpcClient(t, s)
	for _, items := range [][]*pb.PortionRequest{nil, {{FlavorId: b.FlavorID, Portions: 0}}, {{FlavorId: b.FlavorID, Portions: -1}}, {{FlavorId: "bad", Portions: 1}}, {{FlavorId: b.FlavorID, Portions: 1}, {FlavorId: b.FlavorID, Portions: 1}}} {
		_, err := c.CheckAvailability(rpcCtx(t), &pb.CheckAvailabilityRequest{Items: items})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal(items, err)
		}
	}
}
