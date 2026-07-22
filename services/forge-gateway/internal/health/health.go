package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Readiness tracks whether the HTTP server has started accepting connections.
type Readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a readiness gate that starts not-ready.
func NewReadiness() *Readiness {
	return &Readiness{}
}

// MarkReady flips the gate to ready.
func (r *Readiness) MarkReady() {
	r.ready.Store(true)
}

// IsReady reports whether the server has started.
func (r *Readiness) IsReady() bool {
	return r.ready.Load()
}

type response struct {
	Status string `json:"status"`
}

// Handler serves /health/live and /health/ready.
type Handler struct {
	ready *Readiness
}

// NewHandler returns health handlers bound to readiness.
func NewHandler(ready *Readiness) *Handler {
	return &Handler{ready: ready}
}

// Register mounts live/ready routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", h.handleLive)
	mux.HandleFunc("GET /health/ready", h.handleReady)
}

func (h *Handler) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !h.ready.IsReady() {
		writeJSON(w, http.StatusServiceUnavailable, response{Status: "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
