package tests

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"auth-service/internal/auth"
	"auth-service/internal/model"
	"auth-service/internal/router"
	"auth-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type credentialLimitProbe struct {
	calls, active, maximum atomic.Int32
	entered                chan struct{}
	release                <-chan struct{}
}

func (s *credentialLimitProbe) wait(ctx context.Context) error {
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maximum.Load()
		if active <= maximum || s.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *credentialLimitProbe) SignUp(ctx context.Context, _ service.SignUpInput) (*model.User, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return testUser(), nil
}

func (s *credentialLimitProbe) Login(ctx context.Context, _ service.LoginInput) (*service.AuthResult, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return &service.AuthResult{AccessToken: "test-token", TokenType: "Bearer", ExpiresIn: 900, User: testUser()}, nil
}

func (s *credentialLimitProbe) GetCurrentUser(_ context.Context, _ uuid.UUID) (*model.User, error) {
	return testUser(), nil
}

func credentialLimitHTTP(t *testing.T, probe *credentialLimitProbe) (string, *http.Client) {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true, ProxyHeader: "X-Forwarded-For"})
	tokens := auth.NewTokenManager("test_secret_that_is_longer_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", 15*time.Minute)
	router.Setup(app, probe, tokens)
	return serveHardeningHTTP(t, app)
}

func TestSharedHTTPPeerDoesNotCombineIndependentAccountBudgets(t *testing.T) {
	probe := &credentialLimitProbe{}
	baseURL, client := credentialLimitHTTP(t, probe)
	for i := 0; i < 40; i++ {
		endpoint := "login"
		body := fmt.Sprintf(`{"email":"user%d@example.com","password":"password123"}`, i)
		expected := http.StatusOK
		if i%2 == 0 {
			endpoint = "signup"
			body = fmt.Sprintf(`{"email":"user%d@example.com","password":"password123","display_name":"User"}`, i)
			expected = http.StatusCreated
		}
		response, _ := doHardeningJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/"+endpoint, body, "")
		if response.StatusCode != expected {
			t.Fatalf("independent account %d behind one peer was throttled: status=%d", i+1, response.StatusCode)
		}
	}
	if probe.calls.Load() != 40 {
		t.Fatalf("accepted independent accounts reached service %d times, expected 40", probe.calls.Load())
	}
}

func TestAccountBudgetFollowsIdentityAcrossRealTCPPeers(t *testing.T) {
	probe := &credentialLimitProbe{}
	baseURL, firstClient := credentialLimitHTTP(t, probe)
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}, Timeout: time.Second}
	transport := &http.Transport{DialContext: dialer.DialContext, Proxy: nil}
	secondClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	health, err := secondClient.Get(baseURL + "/health")
	if errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Skip("host has no 127.0.0.2 loopback alias; account key independence is covered by limiter unit tests")
	}
	if err != nil {
		t.Fatalf("connect second TCP peer: %v", err)
	}
	io.Copy(io.Discard, health.Body)
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("second peer health request failed: %d", health.StatusCode)
	}
	body := `{"email":"same-account@example.com","password":"password123"}`
	for i := 0; i < 20; i++ {
		response, _ := doHardeningJSON(t, firstClient, http.MethodPost, baseURL+"/api/v1/auth/login", body, "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("first peer account attempt %d failed: %d", i+1, response.StatusCode)
		}
	}
	response, _ := doHardeningJSON(t, secondClient, http.MethodPost, baseURL+"/api/v1/auth/login", body, "")
	if response.StatusCode != http.StatusTooManyRequests || probe.calls.Load() != 20 {
		t.Fatalf("changing TCP peer reset account budget: status=%d calls=%d", response.StatusCode, probe.calls.Load())
	}
}

func TestMalformedCredentialsUseOnlyBoundedPeerTransportBudget(t *testing.T) {
	probe := &credentialLimitProbe{}
	baseURL, client := credentialLimitHTTP(t, probe)
	for i := 0; i <= 600; i++ {
		endpoint := "login"
		if i%2 == 0 {
			endpoint = "signup"
		}
		request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/"+endpoint, strings.NewReader(`{"email":`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.%d.%d", i/250, i%250+1))
		request.Header.Set("X-Real-IP", fmt.Sprintf("198.51.%d.%d", i/250, i%250+1))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		assertSensitiveResponse(t, response)
		expected := http.StatusBadRequest
		if i == 600 {
			expected = http.StatusTooManyRequests
		}
		if response.StatusCode != expected {
			t.Fatalf("malformed request %d: status=%d expected=%d", i+1, response.StatusCode, expected)
		}
		if probe.calls.Load() != 0 {
			t.Fatalf("malformed request reached credential service: calls=%d", probe.calls.Load())
		}
	}
}

func TestInvalidEmailsDoNotAllocateAccountBudgetOrReachService(t *testing.T) {
	probe := &credentialLimitProbe{}
	baseURL, client := credentialLimitHTTP(t, probe)
	for _, email := range []string{"", "not-an-email", "User <user@example.com>", strings.Repeat("x", 250) + "@example.com"} {
		// More than 20 attempts must still produce input errors. A rate-limit
		// response would mean a malformed email had consumed an account key.
		for i := 0; i < 22; i++ {
			endpoint := "login"
			body := fmt.Sprintf(`{"email":%q,"password":"password123"}`, email)
			expected := http.StatusUnauthorized
			if i%2 == 0 {
				endpoint = "signup"
				body = fmt.Sprintf(`{"email":%q,"password":"password123","display_name":"User"}`, email)
				expected = http.StatusBadRequest
			}
			response, _ := doHardeningJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/"+endpoint, body, "")
			if response.StatusCode != expected {
				t.Fatalf("invalid email %q attempt %d: status=%d expected=%d", email, i+1, response.StatusCode, expected)
			}
		}
	}
	if probe.calls.Load() != 0 {
		t.Fatalf("invalid emails reached credential service: calls=%d", probe.calls.Load())
	}
}

func TestCredentialWorkCapacityIsSharedAndRecoversAfterCompletion(t *testing.T) {
	release := make(chan struct{})
	probe := &credentialLimitProbe{entered: make(chan struct{}, 8), release: release}
	baseURL, client := credentialLimitHTTP(t, probe)
	var closeOnce sync.Once
	t.Cleanup(func() { closeOnce.Do(func() { close(release) }) })
	type result struct {
		status int
		err    error
	}
	finished := make(chan result, 8)
	launch := func(i int) {
		go func() {
			endpoint := "login"
			body := fmt.Sprintf(`{"email":"concurrent%d@example.com","password":"password123"}`, i)
			if i%2 == 0 {
				endpoint = "signup"
				body = fmt.Sprintf(`{"email":"concurrent%d@example.com","password":"password123","display_name":"User"}`, i)
			}
			request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/"+endpoint, strings.NewReader(body))
			if err != nil {
				finished <- result{err: err}
				return
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				finished <- result{err: err}
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			finished <- result{status: response.StatusCode}
		}()
	}
	waitEntered := func() {
		t.Helper()
		select {
		case <-probe.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("credential work did not enter service")
		}
	}
	waitCompleted := func() {
		t.Helper()
		select {
		case outcome := <-finished:
			if outcome.err != nil || (outcome.status != http.StatusOK && outcome.status != http.StatusCreated) {
				t.Fatalf("admitted credential work failed: status=%d err=%v", outcome.status, outcome.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("released credential work did not complete")
		}
	}
	for i := 0; i < 4; i++ {
		launch(i)
		waitEntered()
	}
	response, _ := doHardeningJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", `{"email":"overflow@example.com","password":"password123"}`, "")
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "1" || probe.calls.Load() != 4 {
		t.Fatalf("fifth work item was not rejected before service: status=%d retry=%q calls=%d", response.StatusCode, response.Header.Get("Retry-After"), probe.calls.Load())
	}
	release <- struct{}{}
	waitCompleted()
	launch(5)
	waitEntered()
	if probe.calls.Load() != 5 || probe.maximum.Load() != 4 {
		t.Fatalf("shared capacity did not recover correctly: calls=%d max-concurrency=%d", probe.calls.Load(), probe.maximum.Load())
	}
	closeOnce.Do(func() { close(release) })
	for i := 0; i < 4; i++ {
		waitCompleted()
	}
}
