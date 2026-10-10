package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("PORT", "")
	t.Setenv("ORDER_OWNER_TTL", "")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8080" || c.JWTIssuer != "gelatoflow-auth" || c.JWTAudience != "gelatoflow-api" {
		t.Errorf("defaults = %+v", c)
	}
	if c.OrderOwnerTTL != 24*time.Hour || c.HeartbeatInterval != 25*time.Second {
		t.Errorf("durations = %v, %v", c.OrderOwnerTTL, c.HeartbeatInterval)
	}
}

func TestLoadRejectsShortSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "too-short")
	if _, err := Load(); err == nil {
		t.Error("expected an error for a short JWT_SECRET")
	}
}

func TestLoadRejectsBadDuration(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	for _, v := range []string{"soon", "-5s", "0s"} {
		t.Setenv("HEARTBEAT_INTERVAL", v)
		if _, err := Load(); err == nil {
			t.Errorf("HEARTBEAT_INTERVAL=%q should be rejected", v)
		}
	}
}
