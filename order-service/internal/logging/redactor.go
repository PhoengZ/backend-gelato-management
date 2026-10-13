package logging

import (
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const RedactedPlaceholder = "[REDACTED]"

var (
	// Bearer tokens in headers, authorization strings, or logs
	bearerAuthRegex    = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)[A-Za-z0-9\-._~+/]+=*`)
	bearerGenericRegex = regexp.MustCompile(`(?i)(\bBearer\s+)[A-Za-z0-9\-._~+/]+=*`)

	// Sensitive HTTP headers (e.g. X-Payment-Service-Token, X-Order-Service-Token, etc.)
	headerSensitiveRegex = regexp.MustCompile(`(?i)(\b(?:x-payment-service-token|x-order-service-token|x-api-key|x-machine-token):\s*)[^\r\n\s]+`)

	// Sensitive JSON fields
	jsonSensitiveRegex = regexp.MustCompile(`(?i)("(?:password|passwd|pwd|secret|client_secret|jwt_secret|api_key|apiKey|token|machine_token|access_token|refresh_token|private_key|stripe_signature|checkout_session_id|payment_intent_id)"\s*:\s*")[^"]*(")`)

	// Sensitive key-value pairs (query strings, log lines, form data)
	kvSensitiveRegex = regexp.MustCompile(`(?i)(\b(?:password|passwd|pwd|secret|client_secret|jwt_secret|api_key|apiKey|token|machine_token|access_token|refresh_token|private_key|checkout_session_id|payment_intent_id)=)[^\s&"',;]+`)

	// Stripe checkout URLs
	stripeCheckoutURLRegex = regexp.MustCompile(`https://checkout\.stripe\.com/[^\s"'\\]+`)

	// Stripe secret / session IDs in plain text
	stripeSessionIDRegex = regexp.MustCompile(`\b(cs_(?:test|live)_[a-zA-Z0-9]+)\b`)
	stripeIntentIDRegex  = regexp.MustCompile(`\b(pi_[a-zA-Z0-9]+)\b`)
)

// Redact replaces sensitive tokens, machine secrets, Stripe URLs and Stripe session IDs with [REDACTED].
func Redact(input string) string {
	if input == "" {
		return ""
	}

	// 1. Redact Stripe checkout URLs
	result := stripeCheckoutURLRegex.ReplaceAllString(input, "https://checkout.stripe.com/"+RedactedPlaceholder)

	// 2. Redact Stripe IDs
	result = stripeSessionIDRegex.ReplaceAllString(result, RedactedPlaceholder)
	result = stripeIntentIDRegex.ReplaceAllString(result, RedactedPlaceholder)

	// 3. Redact Authorization: Bearer <token>
	result = bearerAuthRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder)

	// 4. Redact standalone Bearer <token> if not already redacted
	result = bearerGenericRegex.ReplaceAllStringFunc(result, func(m string) string {
		if strings.Contains(m, RedactedPlaceholder) {
			return m
		}
		parts := bearerGenericRegex.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return parts[1] + RedactedPlaceholder
		}
		return m
	})

	// 5. Redact sensitive headers
	result = headerSensitiveRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder)

	// 6. Redact sensitive JSON fields
	result = jsonSensitiveRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder+"${2}")

	// 7. Redact key-value pairs
	result = kvSensitiveRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder)

	return result
}

// RedactBytes redacts sensitive data from a byte slice.
func RedactBytes(input []byte) []byte {
	return []byte(Redact(string(input)))
}

// RedactorWriter wraps an io.Writer and intercepts writes to sanitize sensitive information.
type RedactorWriter struct {
	out io.Writer
}

// NewRedactorWriter constructs a RedactorWriter wrapping the given output writer.
func NewRedactorWriter(out io.Writer) *RedactorWriter {
	return &RedactorWriter{out: out}
}

// Write sanitizes sensitive content before passing it to the underlying writer.
func (w *RedactorWriter) Write(p []byte) (n int, err error) {
	redacted := Redact(string(p))
	_, err = w.out.Write([]byte(redacted))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// RedactorLoggerMiddleware logs incoming HTTP requests with sanitized tokens, URLs and headers.
func RedactorLoggerMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		duration := time.Since(start)
		status := c.Response().StatusCode()
		method := c.Method()
		path := c.Path()

		authHeader := Redact(c.Get(fiber.HeaderAuthorization))
		paymentTokenHeader := Redact(c.Get("X-Payment-Service-Token"))
		query := string(c.Request().URI().QueryString())
		if query != "" {
			query = "?" + Redact(query)
		}

		headersInfo := ""
		if authHeader != "" {
			headersInfo += " auth=" + authHeader
		}
		if paymentTokenHeader != "" {
			headersInfo += " payment_token=" + paymentTokenHeader
		}

		logMsg := fmt.Sprintf("[%s] %s%s status=%d duration=%v%s",
			method, path, query, status, duration, headersInfo,
		)
		log.Println(Redact(logMsg))

		return err
	}
}
