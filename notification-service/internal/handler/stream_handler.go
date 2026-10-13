// Package handler serves the health check and the SSE notification stream.
package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"notification-service/internal/hub"
)

// stream holds the connection open and writes one SSE frame per notification.
// If Last-Event-ID is present in headers or query parameters, missed notifications
// are replayed from the ring buffer before streaming live events.
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

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no") // stop nginx-style proxies from buffering the stream
	w.WriteHeader(http.StatusOK)

	// Opening comment flushes headers so the client sees the connection is live.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// Subscribe to live events before replaying to ensure no live events are missed during replay.
	sub, cancel := h.hub.Subscribe(claims.UserID(), claims.Role)
	defer cancel()

	// Track replayed notification IDs to prevent duplicates from live channel.
	replayed := make(map[uuid.UUID]bool)

	// Inspect Last-Event-ID header and query param variations.
	lastEventIDStr := r.Header.Get("Last-Event-ID")
	if lastEventIDStr == "" {
		lastEventIDStr = r.URL.Query().Get("last_event_id")
	}
	if lastEventIDStr == "" {
		lastEventIDStr = r.URL.Query().Get("Last-Event-ID")
	}
	if lastEventIDStr == "" {
		lastEventIDStr = r.URL.Query().Get("lastEventId")
	}

	if lastEventIDStr != "" && h.buffer != nil {
		if lastEventID, err := uuid.Parse(strings.TrimSpace(lastEventIDStr)); err == nil {
			missed := h.buffer.Replay(claims.UserID(), lastEventID)
			for _, n := range missed {
				if err := writeFrame(w, n); err != nil {
					log.Printf("[sse] replay write failed, closing: %v", err)
					return
				}
				flusher.Flush()
				replayed[n.ID] = true
			}
		}
	}

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case n := <-sub.C():
			if replayed[n.ID] {
				continue
			}
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
