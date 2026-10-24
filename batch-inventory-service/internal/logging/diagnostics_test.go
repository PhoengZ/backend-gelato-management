package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLibraryDiagnosticsExcludePayloads(t *testing.T) {
	var output bytes.Buffer
	writer := NewProtocolWriter(&output)
	input := []byte("http2: decoded hpack field private-person@example.invalid Bearer private-token")
	if n, err := writer.Write(input); err != nil || n != len(input) {
		t.Fatal(n, err)
	}
	NewHTTPErrorLogger(&output).Printf("TLS/panic error %s", "private-person@example.invalid")
	if strings.Contains(output.String(), "private-") || !strings.Contains(output.String(), "[HTTP]") || !strings.Contains(output.String(), "[HTTP2]") {
		t.Fatal(output.String())
	}
}
