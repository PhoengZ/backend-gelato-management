// Package handler serves the health check and the SSE notification stream.
package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"notification-service/internal/auth"
	"notification-service/internal/hub"
	"notification-service/internal/stream"
)

type Handler struct {
	verifier  *auth.Verifier
	hub       *hub.Hub
	heartbeat time.Duration
	buffer    *stream.ReplayBuffer
}

// New creates a new Handler. An optional ReplayBuffer can be provided for replaying missed events.
func New(v *auth.Verifier, h *hub.Hub, heartbeat time.Duration, buffer ...*stream.ReplayBuffer) *Handler {
	var buf *stream.ReplayBuffer
	if len(buffer) > 0 {
		buf = buffer[0]
	}
	return &Handler{verifier: v, hub: h, heartbeat: heartbeat, buffer: buf}
}

// WithReplayBuffer attaches a ReplayBuffer to the Handler.
func (h *Handler) WithReplayBuffer(b *stream.ReplayBuffer) *Handler {
	h.buffer = b
	return h
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
