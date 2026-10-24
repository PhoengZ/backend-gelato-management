package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestGRPCLoggerBoundsDiagnosticArgumentsAndHonorsLevels(t *testing.T) {
	t.Setenv("GRPC_GO_LOG_SEVERITY_LEVEL", "warning")
	t.Setenv("GRPC_GO_LOG_VERBOSITY_LEVEL", "2")
	var output bytes.Buffer
	logger := NewGRPCLogger(&output)
	logger.Info("private-info@example.invalid")
	logger.Warningf("bad metadata %s", "private-warning@example.invalid")
	logger.Error("private-error@example.invalid")
	text := output.String()
	if strings.Contains(text, "private-") || strings.Contains(text, "example.invalid") || strings.Contains(text, "severity=info") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "severity=warning") || !strings.Contains(text, "severity=error") || !strings.Contains(text, "source=") {
		t.Fatal(text)
	}
	if !logger.V(2) || logger.V(3) {
		t.Fatal("diagnostic verbosity policy changed")
	}
}
