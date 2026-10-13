package logging

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRedactBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Authorization Bearer header standard",
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.sometoken.signature",
			expected: "Authorization: Bearer [REDACTED]",
		},
		{
			name:     "Authorization Bearer lowercase",
			input:    "authorization: bearer secret-token-12345",
			expected: "authorization: bearer [REDACTED]",
		},
		{
			name:     "Standalone Bearer token",
			input:    "Bearer secret-token-xyz987",
			expected: "Bearer [REDACTED]",
		},
		{
			name:     "Mixed text with Bearer token",
			input:    "Incoming request header 'Authorization: Bearer my-jwt-token' from client",
			expected: "Incoming request header 'Authorization: Bearer [REDACTED]' from client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.expected {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRedactSensitiveJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Password in JSON",
			input:    `{"username": "admin", "password": "supersecretpassword123"}`,
			expected: `{"username": "admin", "password": "[REDACTED]"}`,
		},
		{
			name:     "Machine token in JSON",
			input:    `{"service": "orders", "machine_token": "mt_secret_998877"}`,
			expected: `{"service": "orders", "machine_token": "[REDACTED]"}`,
		},
		{
			name:     "API key and secret in JSON",
			input:    `{"api_key": "live_key_abcdef", "secret": "shhh_secret"}`,
			expected: `{"api_key": "[REDACTED]", "secret": "[REDACTED]"}`,
		},
		{
			name:     "JWT secret and access token",
			input:    `{"jwt_secret": "my-32-byte-secret-key-123456789", "access_token": "tok_123"}`,
			expected: `{"jwt_secret": "[REDACTED]", "access_token": "[REDACTED]"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.expected {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRedactKeyValueAndQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL query params with password and token",
			input:    "https://api.gelatoflow.com/connect?token=tok_abc123&password=secretpass&user=john",
			expected: "https://api.gelatoflow.com/connect?token=[REDACTED]&password=[REDACTED]&user=john",
		},
		{
			name:     "Log line with key=val",
			input:    "failed login attempt with password=plainpassword and api_key=ak_9999",
			expected: "failed login attempt with password=[REDACTED] and api_key=[REDACTED]",
		},
		{
			name:     "Sensitive header line",
			input:    "x-api-key: secret_api_key_value",
			expected: "x-api-key: [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.expected {
				t.Errorf("Redact(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRedactorWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := NewRedactorWriter(&buf)

	input := "User logged in with Authorization: Bearer token123 and password=mypassword\n"
	n, err := writer.Write([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Fatalf("expected written len %d, got %d", len(input), n)
	}

	expected := "User logged in with Authorization: Bearer [REDACTED] and password=[REDACTED]\n"
	if buf.String() != expected {
		t.Errorf("RedactorWriter output = %q, want %q", buf.String(), expected)
	}
}

func TestRedactorLoggerMiddleware(t *testing.T) {
	app := fiber.New()
	app.Use(RedactorLoggerMiddleware())

	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test?token=secrettoken123", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.test.sig")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", string(body))
	}
}
