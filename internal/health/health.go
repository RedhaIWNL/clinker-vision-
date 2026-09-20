package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type Handler struct {
	ready     atomic.Bool
	startedAt time.Time
}

func New() *Handler {
	return &Handler{startedAt: time.Now().UTC()}
}

func (h *Handler) SetReady(ready bool) {
	h.ready.Store(ready)
}

func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"started_at": h.startedAt.Format(time.RFC3339Nano),
	})
}

func (h *Handler) Ready(w http.ResponseWriter, _ *http.Request) {
	if !h.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
