package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Checker reports dependency readiness (optional).
type Checker interface {
	Ready(ctx context.Context) error
}

// Readiness tracks whether the HTTP server has started accepting connections.
type Readiness struct {
	ready   atomic.Bool
	checker Checker
}

// NewReadiness returns a readiness gate that starts not-ready.
func NewReadiness(checker Checker) *Readiness {
	return &Readiness{checker: checker}
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

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if !h.ready.IsReady() {
		writeJSON(w, http.StatusServiceUnavailable, response{Status: "not_ready"})
		return
	}
	if h.ready.checker != nil {
		if err := h.ready.checker.Ready(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, response{Status: "not_ready"})
			return
		}
	}
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
