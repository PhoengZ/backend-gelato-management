// Package handler serves the health check and the SSE notification stream.
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"notification-service/internal/auth"
	"notification-service/internal/hub"
)

type Handler struct {
	verifier  *auth.Verifier
	hub       *hub.Hub
	heartbeat time.Duration
}

func New(v *auth.Verifier, h *hub.Hub, heartbeat time.Duration) *Handler {
	return &Handler{verifier: v, hub: h, heartbeat: heartbeat}
}

// Routes returns the service router.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /api/v1/notifications/stream", h.stream)
	return mux
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// stream holds the connection open and writes one SSE frame per notification.
// Customers receive only notifications for their own orders; staff and managers
// receive inventory alerts. Nothing is replayed on reconnect.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	claims, err := h.authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "A valid Bearer access token is required.")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Streaming is not supported.")
		return
	}

	sub, cancel := h.hub.Subscribe(claims.UserID(), claims.Role)
	defer cancel()

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no") // stop nginx-style proxies from buffering the stream
	w.WriteHeader(http.StatusOK)

	// Opening comment flushes headers so the client sees the connection is live.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n := <-sub.C():
			if err := writeFrame(w, n); err != nil {
				log.Printf("[sse] write failed, closing: %v", err)
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeFrame(w http.ResponseWriter, n hub.Notification) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", n.ID, n.Type, data)
	return err
}

// authenticate requires exactly one "Authorization: Bearer <jwt>" header.
func (h *Handler) authenticate(r *http.Request) (*auth.Claims, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return nil, auth.ErrInvalidToken
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return nil, auth.ErrInvalidToken
	}
	return h.verifier.Parse(strings.TrimSpace(token))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError emits the structured error shape from AGENTS.md: code, message, optional details.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
