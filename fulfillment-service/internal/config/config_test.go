package config

import (
	"strings"
	"testing"
)

func TestValidateJWTSecret(t *testing.T) {
	for name, tc := range map[string]struct {
		secret  string
		wantErr bool
	}{
		"empty":     {"", true},
		"too short": {strings.Repeat("a", MinimumJWTSecretBytes-1), true},
		"minimum":   {strings.Repeat("a", MinimumJWTSecretBytes), false},
	} {
		t.Run(name, func(t *testing.T) {
			err := Config{JWTSecret: tc.secret}.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
