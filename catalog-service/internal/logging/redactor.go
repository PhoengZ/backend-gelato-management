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

	// Sensitive JSON fields (password, token, secret, machine token, api key, etc.)
	jsonSensitiveRegex = regexp.MustCompile(`(?i)("(?:password|passwd|pwd|secret|client_secret|jwt_secret|api_key|apiKey|token|machine_token|access_token|refresh_token|private_key|stripe_signature|credit_card)"\s*:\s*")[^"]*(")`)

	// Sensitive key-value pairs (query strings, log lines, form data)
	kvSensitiveRegex = regexp.MustCompile(`(?i)(\b(?:password|passwd|pwd|secret|client_secret|jwt_secret|api_key|apiKey|token|machine_token|access_token|refresh_token|private_key)=)[^\s&"',;]+`)

	// Sensitive headers or YAML-like key: value
	headerSensitiveRegex = regexp.MustCompile(`(?i)(\b(?:x-api-key|x-machine-token|machine-token|client-secret|jwt-secret):\s*)[^\r\n\s]+`)
)

// Redact replaces sensitive tokens, passwords, machine secrets, and keys with [REDACTED].
func Redact(input string) string {
	if input == "" {
		return ""
	}

	// 1. Redact Authorization: Bearer <token>
	result := bearerAuthRegex.ReplaceAllString(input, "${1}"+RedactedPlaceholder)

	// 2. Redact standalone Bearer <token> if not already redacted
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

	// 3. Redact JSON fields
	result = jsonSensitiveRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder+"${2}")

	// 4. Redact Key-Value pairs
	result = kvSensitiveRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder)

	// 5. Redact sensitive headers
	result = headerSensitiveRegex.ReplaceAllString(result, "${1}"+RedactedPlaceholder)

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

// RedactorLoggerMiddleware logs incoming HTTP requests with sanitized tokens and secrets.
func RedactorLoggerMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		duration := time.Since(start)
		status := c.Response().StatusCode()
		method := c.Method()
		path := c.Path()

		authHeader := Redact(c.Get(fiber.HeaderAuthorization))
		query := string(c.Request().URI().QueryString())
		if query != "" {
			query = "?" + Redact(query)
		}

		logMsg := fmt.Sprintf("[%s] %s%s status=%d duration=%v auth=%s",
			method, path, query, status, duration, authHeader,
		)
		log.Println(Redact(logMsg))

		return err
	}
}
