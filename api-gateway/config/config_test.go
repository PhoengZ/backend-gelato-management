package config

import (
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		jwtSecret string
		wantErr   bool
	}{
		{
			name:      "empty secret fails",
			jwtSecret: "",
			wantErr:   true,
		},
		{
			name:      "secret shorter than 32 bytes fails",
			jwtSecret: "short_secret_under_32_bytes",
			wantErr:   true,
		},
		{
			name:      "secret exactly 32 bytes passes",
			jwtSecret: strings.Repeat("a", 32),
			wantErr:   false,
		},
		{
			name:      "secret longer than 32 bytes passes",
			jwtSecret: "a_very_long_and_secure_jwt_signing_secret_for_gelatoflow",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				JWTSecret: tt.jwtSecret,
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg := LoadConfig()
	if cfg.JWTIssuer == "" {
		t.Errorf("expected default JWTIssuer, got empty")
	}
	if cfg.JWTAudience == "" {
		t.Errorf("expected default JWTAudience, got empty")
	}
}
