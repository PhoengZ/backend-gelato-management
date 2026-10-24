package logging

import (
	"bytes"
	"io"
	"log"
)

// ProtocolWriter removes x/net/http2 debug payloads from the standard logger.
type ProtocolWriter struct{ output io.Writer }

func NewProtocolWriter(output io.Writer) *ProtocolWriter { return &ProtocolWriter{output} }
func (w *ProtocolWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("http2:")) {
		_, err := w.output.Write([]byte("[HTTP2] event=transport_diagnostic\n"))
		if err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return w.output.Write(p)
}

// net/http may log peer details, arbitrary panic text or TLS handshake reasons.
// Retain the diagnostic event without copying those request-controlled values.
type httpErrorWriter struct{ output io.Writer }

func (w httpErrorWriter) Write(p []byte) (int, error) {
	_, err := w.output.Write([]byte("[HTTP] event=server_diagnostic\n"))
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
func NewHTTPErrorLogger(output io.Writer) *log.Logger { return log.New(httpErrorWriter{output}, "", 0) }
