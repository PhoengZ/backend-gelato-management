package proxy

import (
	"bufio"
	"io"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
)

// SSEProxy proxies Server-Sent Events (SSE) connections to upstream services
// without buffering, flushing each chunk to the client immediately.
type SSEProxy struct {
	client    *fasthttp.Client
	targetURL string
}

// NewSSEProxy creates a new SSEProxy targeting the specified upstream base URL.
func NewSSEProxy(targetURL string) *SSEProxy {
	return NewSSEProxyWithClient(targetURL, &fasthttp.Client{
		StreamResponseBody:            true,
		NoDefaultUserAgentHeader:      true,
		DisableHeaderNamesNormalizing: false,
		ReadTimeout:                   0, // No timeout for long-lived streaming
		WriteTimeout:                  10 * time.Second,
	})
}

// NewSSEProxyWithClient creates a new SSEProxy with a customized fasthttp.Client.
func NewSSEProxyWithClient(targetURL string, client *fasthttp.Client) *SSEProxy {
	return &SSEProxy{
		targetURL: strings.TrimRight(targetURL, "/"),
		client:    client,
	}
}

// Handler returns a fiber.Handler that unbuffered-proxies SSE connections to the upstream service.
func (p *SSEProxy) Handler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		upstreamURL := p.targetURL + c.OriginalURL()

		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()

		req.SetRequestURI(upstreamURL)
		req.Header.SetMethod(c.Method())

		// Forward headers from client request
		c.Request().Header.VisitAll(func(key, val []byte) {
			k := string(key)
			if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Upgrade") {
				return
			}
			req.Header.SetBytesKV(key, val)
		})

		// Forward request body if present
		if body := c.Body(); len(body) > 0 {
			req.SetBody(body)
		}

		err := p.client.Do(req, resp)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"code":    "BAD_GATEWAY",
				"message": "Failed to connect to notification service",
			})
		}

		statusCode := resp.StatusCode()
		c.Status(statusCode)

		// Forward upstream response headers
		resp.Header.VisitAll(func(key, val []byte) {
			k := string(key)
			if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Transfer-Encoding") {
				return
			}
			c.Set(k, string(val))
		})

		// If upstream returned a non-200 error, forward body directly
		if statusCode != fiber.StatusOK {
			body := append([]byte(nil), resp.Body()...)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			return c.Send(body)
		}

		// Ensure SSE headers are present
		c.Set(fiber.HeaderContentType, "text/event-stream")
		c.Set(fiber.HeaderCacheControl, "no-cache")
		c.Set(fiber.HeaderConnection, "keep-alive")

		bodyStream := resp.BodyStream()
		if bodyStream == nil {
			body := append([]byte(nil), resp.Body()...)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
			return c.Send(body)
		}

		// Stream response body chunk-by-chunk with immediate flushing
		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			defer func() {
				if closer, ok := bodyStream.(io.Closer); ok {
					_ = closer.Close()
				}
				fasthttp.ReleaseRequest(req)
				fasthttp.ReleaseResponse(resp)
			}()

			buf := make([]byte, 4096)
			for {
				n, readErr := bodyStream.Read(buf)
				if n > 0 {
					if _, writeErr := w.Write(buf[:n]); writeErr != nil {
						return
					}
					if flushErr := w.Flush(); flushErr != nil {
						return
					}
				}
				if readErr != nil {
					return
				}
			}
		})

		return nil
	}
}
