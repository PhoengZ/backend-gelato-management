package router

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gelato/api-gateway/config"
	"github.com/gofiber/fiber/v2"
)

func clientIP(t *testing.T, trusted []string, forwardedFor string) string {
	t.Helper()
	app := fiber.New(AppConfig(config.Config{TrustedProxies: trusted}))
	app.Get("/ip", func(c *fiber.Ctx) error { return c.SendString(c.IP()) })
	req := httptest.NewRequest("GET", "/ip", nil)
	req.Header.Set("X-Forwarded-For", forwardedFor)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestClientIPComesFromForwardedForOnlyBehindATrustedProxy(t *testing.T) {
	const client = "203.0.113.9"
	if got := clientIP(t, []string{"0.0.0.0/32"}, client); got != client {
		t.Errorf("behind trusted proxy IP = %q, want %q", got, client)
	}
	if got := clientIP(t, []string{"10.0.0.0/8"}, client); got == client {
		t.Error("untrusted peer was able to spoof its client IP")
	}
	if got := clientIP(t, nil, client); got == client {
		t.Error("X-Forwarded-For must be ignored when no proxy is trusted")
	}
}
