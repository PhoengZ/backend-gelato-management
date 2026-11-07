package proxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// streamingUpstream emits a frame every few milliseconds until the proxy drops
// the connection, and reports when each stream's request context ends.
func streamingUpstream(t *testing.T, closed chan<- struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				select {
				case closed <- struct{}{}:
				default:
				}
				return
			case <-ticker.C:
				if _, err := fmt.Fprint(w, "data: frame\n\n"); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func serveProxy(t *testing.T, upstreamURL string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/stream", NewSSEProxy(upstreamURL).Handler())
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return ln.Addr().String()
}

// openStream connects with a raw socket, so closing it is a real client disconnect.
func openStream(t *testing.T, addr string) (net.Conn, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET /stream HTTP/1.1\r\nHost: gateway\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for a frame: %v", err)
		}
		if strings.Contains(line, "data: frame") {
			return conn, status
		}
	}
}

func TestSSEProxy_ClientDisconnectClosesUpstreamConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	addr := serveProxy(t, streamingUpstream(t, closed).URL)

	conn, _ := openStream(t, addr)
	_ = conn.Close()

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream stream stayed open after the client disconnected; its connection must not return to the idle pool")
	}
}

func TestSSEProxy_ReconnectAfterDisconnectStreamsAgain(t *testing.T) {
	closed := make(chan struct{}, 1)
	addr := serveProxy(t, streamingUpstream(t, closed).URL)

	first, _ := openStream(t, addr)
	_ = first.Close()
	time.Sleep(200 * time.Millisecond) // let the proxy observe the failed write and clean up

	second, status := openStream(t, addr)
	defer second.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("reconnect status = %q, want 200", status)
	}
}

func TestSSEProxy_UpstreamEndThenReconnect(t *testing.T) {
	var streams atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streams.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: frame\n\n")
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(upstream.Close)
	addr := serveProxy(t, upstream.URL)

	for i := 0; i < 3; i++ {
		conn, status := openStream(t, addr)
		_ = conn.Close()
		if !strings.Contains(status, "200") {
			t.Fatalf("attempt %d status = %q, want 200", i, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := streams.Load(); got != 3 {
		t.Fatalf("upstream streams = %d, want 3", got)
	}
}
