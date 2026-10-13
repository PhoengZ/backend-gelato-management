package logging

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
		omits    string
	}{
		{
			name:     "Bearer Authorization header",
			input:    "Authorization: Bearer secret_jwt_token_12345",
			contains: "Authorization: Bearer [REDACTED]",
			omits:    "secret_jwt_token_12345",
		},
		{
			name:     "Standalone Bearer token",
			input:    "Token is Bearer my-secret-token",
			contains: "Bearer [REDACTED]",
			omits:    "my-secret-token",
		},
		{
			name:     "X-Payment-Service-Token header",
			input:    "x-payment-service-token: super-secret-callback-token-32-bytes",
			contains: "x-payment-service-token: [REDACTED]",
			omits:    "super-secret-callback-token-32-bytes",
		},
		{
			name:     "X-Order-Service-Token header",
			input:    "X-Order-Service-Token: internal-order-token-value",
			contains: "X-Order-Service-Token: [REDACTED]",
			omits:    "internal-order-token-value",
		},
		{
			name:     "Stripe checkout URL",
			input:    `{"checkout_url":"https://checkout.stripe.com/c/pay/cs_test_a1b2c3d4e5"}`,
			contains: "https://checkout.stripe.com/[REDACTED]",
			omits:    "cs_test_a1b2c3d4e5",
		},
		{
			name:     "Stripe session ID in text",
			input:    "Created checkout session cs_test_1234567890abcdef for order",
			contains: "[REDACTED]",
			omits:    "cs_test_1234567890abcdef",
		},
		{
			name:     "Stripe payment intent ID in text",
			input:    "Received payment intent pi_9876543210 fed to worker",
			contains: "[REDACTED]",
			omits:    "pi_9876543210",
		},
		{
			name:     "JSON sensitive password and secret",
			input:    `{"user":"alice","password":"mypassword123","secret":"topsecret"}`,
			contains: `{"user":"alice","password":"[REDACTED]","secret":"[REDACTED]"}`,
			omits:    "mypassword123",
		},
		{
			name:     "Key-value secret in query or form",
			input:    "GET /webhook?token=mytoken123&payment_intent_id=pi_abc",
			contains: "token=[REDACTED]",
			omits:    "mytoken123",
		},
		{
			name:     "Empty input",
			input:    "",
			contains: "",
			omits:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Errorf("Redact(%q) = %q; want it to contain %q", tt.input, got, tt.contains)
			}
			if tt.omits != "" && strings.Contains(got, tt.omits) {
				t.Errorf("Redact(%q) = %q; should NOT contain %q", tt.input, got, tt.omits)
			}
		})
	}
}

func TestRedactBytes(t *testing.T) {
	input := []byte("Authorization: Bearer my-secret-token")
	redacted := RedactBytes(input)
	if strings.Contains(string(redacted), "my-secret-token") {
		t.Fatalf("RedactBytes failed to sanitize token: %s", string(redacted))
	}
}

func TestRedactorWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := NewRedactorWriter(&buf)

	msg := "Log message with password=supersecret and https://checkout.stripe.com/c/pay/cs_test_123"
	n, err := writer.Write([]byte(msg))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("got written bytes %d, want %d", n, len(msg))
	}

	out := buf.String()
	if strings.Contains(out, "supersecret") || strings.Contains(out, "cs_test_123") {
		t.Errorf("RedactorWriter leaked sensitive info: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output: %s", out)
	}
}

func TestRedactorLoggerMiddleware(t *testing.T) {
	app := fiber.New()
	app.Use(RedactorLoggerMiddleware())
	app.Get("/test", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test?token=supersecret123", nil)
	req.Header.Set("Authorization", "Bearer secret-jwt-abc")
	req.Header.Set("X-Payment-Service-Token", "secret-payment-token-xyz")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
}
