package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/auth"
	"catalog-service/internal/model"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

// Build and launch the actual entry point, then restart against the same Redis.
// This complements transport tests: a contract and test-only server are not a
// runnable service. Redis cleanup remains scoped to the random test namespace.
func TestCatalogProcessRestartRetainsGRPCData(t *testing.T) {
	_, _, prefix := redisRepositoryForTest(t)
	bin := filepath.Join(t.TempDir(), "catalog-service")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-race", "-o", bin, "../cmd/api")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	var id string
	for cycle := range 2 {
		httpAddr, grpcAddr := availableAddress(t), availableAddress(t)
		_, httpPort, _ := net.SplitHostPort(httpAddr)
		env := append(os.Environ(), "PORT="+httpPort, "GRPC_ADDR="+grpcAddr,
			"REDIS_URL="+os.Getenv("TEST_REDIS_URL"), "REDIS_KEY_PREFIX="+prefix,
			"JWT_SECRET="+handlerTestSecret, "JWT_ISSUER=gelatoflow-auth", "JWT_AUDIENCE=gelatoflow-api", "REQUEST_TIMEOUT=1s")
		stop := startCatalogProcess(t, bin, env, httpAddr)
		conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		client := pb.NewCatalogServiceClient(conn)
		ctx := grpcContext(t, handlerToken(t, auth.RoleManager))
		if cycle == 0 {
			created, err := client.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: grpcInput("Survives restart")})
			if err != nil {
				t.Fatal(err)
			}
			id = created.Flavor.Flavor.Id
		} else {
			got, err := client.GetFlavor(grpcContext(t, ""), &pb.GetFlavorRequest{FlavorId: id})
			if err != nil || got.Flavor.Name != "Survives restart" {
				t.Fatalf("restart lost data: %v %v", got, err)
			}
		}
		httpClient := &http.Client{Timeout: time.Second}
		response, err := httpClient.Get("http://" + httpAddr + "/api/v1/flavors/" + id)
		if err != nil {
			t.Fatal(err)
		}
		var flavor model.Flavor
		err = json.NewDecoder(response.Body).Decode(&flavor)
		response.Body.Close()
		httpClient.CloseIdleConnections()
		if err != nil || response.StatusCode != 200 || flavor.ID.String() != id {
			t.Fatalf("HTTP/gRPC process mismatch: %v", err)
		}
		if cycle == 1 {
			_, err = client.DeleteFlavor(ctx, &pb.DeleteFlavorRequest{FlavorId: id})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetFlavor(grpcContext(t, ""), &pb.GetFlavorRequest{FlavorId: id})
			wantCode(t, err, codes.NotFound)
		}
		conn.Close()
		if err := stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func availableAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	return address
}

func startCatalogProcess(t *testing.T, binary string, env []string, httpAddr string) func() error {
	t.Helper()
	logFile, err := os.Create(filepath.Join(t.TempDir(), "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logFile.Close() })
	cmd := exec.Command(binary)
	cmd.Env = env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	var once sync.Once
	var stopErr error
	stop := func() error {
		once.Do(func() {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
				stopErr = waitErr
			case <-time.After(7 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				stopErr = fmt.Errorf("Catalog process did not stop within 7s")
			}
			if stopErr != nil {
				data, _ := os.ReadFile(logFile.Name())
				stopErr = fmt.Errorf("%w: %s", stopErr, data)
			}
		})
		return stopErr
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Timeout: 150 * time.Millisecond}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			data, _ := os.ReadFile(logFile.Name())
			t.Fatalf("process exited at startup: %v %s", waitErr, data)
		default:
		}
		resp, err := client.Get("http://" + httpAddr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return stop
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Catalog process did not become ready")
	return stop
}
