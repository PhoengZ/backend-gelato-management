package tests

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"auth-service/internal/auth"
	"auth-service/internal/model"
	"auth-service/internal/repository"
	"auth-service/internal/router"
	"auth-service/internal/service"
	"auth-service/migrations"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type hardeningProbe struct {
	credentialCalls int
	deadline        time.Time
}

func (s *hardeningProbe) SignUp(ctx context.Context, _ service.SignUpInput) (*model.User, error) {
	s.credentialCalls++
	s.deadline, _ = ctx.Deadline()
	return testUser(), nil
}

func (s *hardeningProbe) Login(ctx context.Context, _ service.LoginInput) (*service.AuthResult, error) {
	s.credentialCalls++
	s.deadline, _ = ctx.Deadline()
	return &service.AuthResult{AccessToken: "test-token", TokenType: "Bearer", ExpiresIn: 900, User: testUser()}, nil
}

func (s *hardeningProbe) GetCurrentUser(_ context.Context, _ uuid.UUID) (*model.User, error) {
	return testUser(), nil
}

func TestCredentialTransportAndSchemaBoundaries(t *testing.T) {
	login := `{"email":"user@example.com","password":"password123"}`
	signup := `{"email":"user@example.com","password":"password123","display_name":"User"}`
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(login)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, endpoint, body, contentType, encoding string
		status                                      int
	}{
		{name: "login duplicate password", endpoint: "login", body: `{"email":"user@example.com","password":"wrong","password":"password123"}`, contentType: "application/json", status: 400},
		{name: "escaped duplicate field", endpoint: "signup", body: `{"email":"user@example.com","\u0065mail":"other@example.com","password":"password123","display_name":"User"}`, contentType: "application/json", status: 400},
		{name: "case alias", endpoint: "login", body: `{"email":"user@example.com","PASSWORD":"password123"}`, contentType: "application/json", status: 400},
		{name: "null credential", endpoint: "login", body: `{"email":"user@example.com","password":null}`, contentType: "application/json", status: 400},
		{name: "number credential", endpoint: "login", body: `{"email":"user@example.com","password":12345678}`, contentType: "application/json", status: 400},
		{name: "nested credential", endpoint: "login", body: `{"email":"user@example.com","password":{"value":"password123"}}`, contentType: "application/json", status: 400},
		{name: "missing credential", endpoint: "login", body: `{"email":"user@example.com"}`, contentType: "application/json", status: 400},
		{name: "unknown login field", endpoint: "login", body: `{"email":"user@example.com","password":"password123","role":"MANAGER"}`, contentType: "application/json", status: 400},
		{name: "array instead of object", endpoint: "login", body: `[]`, contentType: "application/json", status: 400},
		{name: "multiple objects", endpoint: "login", body: login + login, contentType: "application/json", status: 400},
		{name: "missing media type", endpoint: "login", body: login, status: 415},
		{name: "plain text media type", endpoint: "signup", body: signup, contentType: "text/plain", status: 415},
		{name: "form media type", endpoint: "login", body: login, contentType: "application/x-www-form-urlencoded", status: 415},
		{name: "unsupported compression", endpoint: "login", body: compressed.String(), contentType: "application/json", encoding: "gzip", status: 415},
		{name: "oversized body", endpoint: "signup", body: strings.Repeat("x", 16*1024+1), contentType: "application/json", status: 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &hardeningProbe{}
			app, _ := newHandlerApp(probe)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+tc.endpoint, strings.NewReader(tc.body))
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			if tc.encoding != "" {
				request.Header.Set("Content-Encoding", tc.encoding)
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status || probe.credentialCalls != 0 {
				t.Fatalf("status=%d, expected=%d; credential service calls=%d", response.StatusCode, tc.status, probe.credentialCalls)
			}
			assertSensitiveResponse(t, response)
		})
	}
}

func TestCredentialHandlersReceiveBoundedContext(t *testing.T) {
	for _, tc := range []struct{ endpoint, body string }{
		{"signup", `{"email":"user@example.com","password":"password123","display_name":"User"}`},
		{"login", `{"email":"user@example.com","password":"password123"}`},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			probe := &hardeningProbe{}
			app, _ := newHandlerApp(probe)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+tc.endpoint, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode >= 300 || probe.credentialCalls != 1 {
				t.Fatalf("valid JSON request failed: status=%d calls=%d", response.StatusCode, probe.credentialCalls)
			}
			remaining := time.Until(probe.deadline)
			if probe.deadline.IsZero() || remaining <= 0 || remaining > 30*time.Second {
				t.Fatalf("credential request has no usable bounded deadline: %v", probe.deadline)
			}
			assertSensitiveResponse(t, response)
		})
	}
}

func TestDirectHTTPRateLimitSharesCanonicalAccountBudgetAcrossCredentialRoutes(t *testing.T) {
	probe := &hardeningProbe{}
	tokens := auth.NewTokenManager("test_secret_that_is_longer_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", 15*time.Minute)
	// Signup and login share one budget for the normalized email, regardless
	// of capitalization, surrounding whitespace, or caller-selected IP headers.
	app := fiber.New(fiber.Config{DisableStartupMessage: true, ProxyHeader: "X-Forwarded-For"})
	router.Setup(app, probe, tokens)
	baseURL, client := serveHardeningHTTP(t, app)
	emails := []string{"user@example.com", "USER@EXAMPLE.COM", "  User@Example.Com  "}
	for i := 0; i < 22; i++ {
		email := emails[i%len(emails)]
		endpoint, body, expected := "login", fmt.Sprintf(`{"email":%q,"password":"password123"}`, email), http.StatusOK
		if i%2 == 0 {
			endpoint, body, expected = "signup", fmt.Sprintf(`{"email":%q,"password":"password123","display_name":"User"}`, email), http.StatusCreated
		}
		if i >= 20 {
			expected = http.StatusTooManyRequests
		}
		request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/"+endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i+1))
		request.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i+1))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("credential attempt %d: status=%d expected=%d", i+1, response.StatusCode, expected)
		}
		assertSensitiveResponse(t, response)
		if expected == http.StatusTooManyRequests {
			seconds, err := strconv.Atoi(response.Header.Get("Retry-After"))
			if err != nil || seconds <= 0 || seconds > 60 {
				t.Fatalf("invalid Retry-After: %q", response.Header.Get("Retry-After"))
			}
		}
	}
	token, err := tokens.Issue(testUser())
	if err != nil {
		t.Fatal(err)
	}
	response, _ := doHardeningJSON(t, client, http.MethodGet, baseURL+"/api/v1/auth/me", "", "Bearer "+token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("credential limit blocked authenticated profile access: %d", response.StatusCode)
	}
}

func TestAuthHTTPPostgresAccountLifecycle(t *testing.T) {
	pool := hardeningDatabase(t)
	tokens := auth.NewTokenManager("test_secret_that_is_longer_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", 15*time.Minute)
	authService, err := service.NewAuthService(repository.NewPostgresUserRepository(pool), tokens, bcrypt.MinCost)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	router.Setup(app, authService, tokens)
	baseURL, client := serveHardeningHTTP(t, app)
	baseURL += "/api/v1/auth"
	password := strings.Repeat("ก", 8)
	signupBody := fmt.Sprintf(`{"email":"  HTTP.User@Example.com  ","password":%q,"display_name":" HTTP User "}`, password)
	response, payload := doHardeningJSON(t, client, http.MethodPost, baseURL+"/signup", signupBody, "")
	if response.StatusCode != http.StatusCreated || payload["role"] != "CUSTOMER" || payload["status"] != "ACTIVE" || payload["email"] != "http.user@example.com" || payload["display_name"] != "HTTP User" {
		t.Fatalf("signup did not create normalized customer: status=%d payload=%+v", response.StatusCode, payload)
	}
	userID, err := uuid.Parse(fmt.Sprint(payload["id"]))
	if err != nil || userID == uuid.Nil {
		t.Fatalf("signup returned invalid user ID: %v", payload["id"])
	}
	var hash, storedRole string
	if err := pool.QueryRow(context.Background(), "SELECT password_hash, role FROM users WHERE id = $1", userID).Scan(&hash, &storedRole); err != nil {
		t.Fatal(err)
	}
	if hash == password || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || storedRole != "CUSTOMER" {
		t.Fatal("created account did not persist a matching bcrypt hash and customer role")
	}
	response, _ = doHardeningJSON(t, client, http.MethodPost, baseURL+"/signup", signupBody, "")
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("normalized duplicate email was not rejected: %d", response.StatusCode)
	}
	for _, badPassword := range []string{strings.Repeat("ก", 3), strings.Repeat("ก", 25)} {
		body := fmt.Sprintf(`{"email":"short@example.com","password":%q,"display_name":"Short"}`, badPassword)
		response, _ = doHardeningJSON(t, client, http.MethodPost, baseURL+"/signup", body, "")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid Unicode password length accepted: runes=%d bytes=%d status=%d", len([]rune(badPassword)), len(badPassword), response.StatusCode)
		}
	}
	loginBody := fmt.Sprintf(`{"email":"HTTP.USER@EXAMPLE.COM","password":%q}`, password)
	response, payload = doHardeningJSON(t, client, http.MethodPost, baseURL+"/login", loginBody, "")
	if response.StatusCode != http.StatusOK || payload["token_type"] != "Bearer" || payload["expires_in"] != float64(900) {
		t.Fatalf("login failed: status=%d payload=%+v", response.StatusCode, payload)
	}
	token, ok := payload["access_token"].(string)
	if !ok || token == "" {
		t.Fatal("login did not return access token")
	}
	claims, err := tokens.Parse(token)
	if err != nil || claims.Subject != userID.String() || claims.Role != model.RoleCustomer {
		t.Fatalf("login token has incorrect identity: claims=%+v err=%v", claims, err)
	}
	response, payload = doHardeningJSON(t, client, http.MethodGet, baseURL+"/me", "", "Bearer "+token)
	if response.StatusCode != http.StatusOK || payload["id"] != userID.String() {
		t.Fatalf("me did not return signed-in database identity: status=%d payload=%+v", response.StatusCode, payload)
	}
	request, err := http.NewRequest(http.MethodGet, baseURL+"/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Add("Authorization", "Bearer "+token)
	request.Header.Add("Authorization", "Bearer "+token)
	duplicateResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, duplicateResponse.Body)
	duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("real HTTP duplicate Authorization accepted: %d", duplicateResponse.StatusCode)
	}
	assertSensitiveResponse(t, duplicateResponse)
	response, wrongPassword := doHardeningJSON(t, client, http.MethodPost, baseURL+"/login", `{"email":"http.user@example.com","password":"incorrect"}`, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password was accepted: %d", response.StatusCode)
	}
	response, unknownAccount := doHardeningJSON(t, client, http.MethodPost, baseURL+"/login", `{"email":"unknown@example.com","password":"incorrect"}`, "")
	if response.StatusCode != http.StatusUnauthorized || wrongPassword["code"] != unknownAccount["code"] || wrongPassword["message"] != unknownAccount["message"] {
		t.Fatalf("unknown account disclosed a distinct error: wrong=%+v unknown=%+v status=%d", wrongPassword, unknownAccount, response.StatusCode)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE users SET status = 'DISABLED' WHERE id = $1", userID); err != nil {
		t.Fatal(err)
	}
	response, disabledAccount := doHardeningJSON(t, client, http.MethodPost, baseURL+"/login", loginBody, "")
	if response.StatusCode != http.StatusUnauthorized || disabledAccount["code"] != wrongPassword["code"] || disabledAccount["message"] != wrongPassword["message"] {
		t.Fatalf("disabled account login has incorrect response: status=%d payload=%+v", response.StatusCode, disabledAccount)
	}
	response, payload = doHardeningJSON(t, client, http.MethodGet, baseURL+"/me", "", "Bearer "+token)
	if response.StatusCode != http.StatusUnauthorized || payload["code"] != "UNAUTHORIZED" {
		t.Fatalf("disabled account retained me access: status=%d payload=%+v", response.StatusCode, payload)
	}
}

func assertSensitiveResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("sensitive response is cacheable: %q", response.Header.Get("Cache-Control"))
	}
	for _, value := range response.Header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Authorization") {
				return
			}
		}
	}
	t.Fatal("sensitive response does not vary by Authorization")
}

func serveHardeningHTTP(t *testing.T, app *fiber.App) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- app.Listener(listener) }()
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		if err := app.ShutdownWithTimeout(3 * time.Second); err != nil {
			t.Errorf("stop auth test server: %v", err)
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("auth test server failed: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("auth test server did not stop")
		}
	})
	return "http://" + listener.Addr().String(), client
}

func doHardeningJSON(t *testing.T, client *http.Client, method, url, body, authorization string) (*http.Response, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertSensitiveResponse(t, response)
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode auth HTTP response (%d): %v", response.StatusCode, err)
	}
	if _, leaked := payload["password_hash"]; leaked {
		t.Fatal("response leaked credential hash")
	}
	if user, ok := payload["user"].(map[string]any); ok {
		if _, leaked := user["password_hash"]; leaked {
			t.Fatal("login user response leaked credential hash")
		}
	}
	return response, payload
}

func hardeningDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create database pool: %v", err)
	}
	schema := pgx.Identifier{"auth_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create isolated HTTP test schema: %v", err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean isolated HTTP test schema: %v", err)
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+schema)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("migrate isolated HTTP test schema: %v", err)
	}
	return pool
}
