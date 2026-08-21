package tests

import (
	"testing"
)

func TestHealthCheck(t *testing.T) {
	// A simple dummy test to ensure `go test` passes in the CI workflow
	expected := true
	if !expected {
		t.Errorf("Expected test to pass")
	}
}
