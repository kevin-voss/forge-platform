package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// ReadyChecker reports whether Discovery can serve traffic.
type ReadyChecker interface {
	Ready(ctx context.Context) error
}

// Readiness tracks DB + Control kind-registration readiness.
type Readiness struct {
	kindsRegistered atomic.Bool
	db              ReadyChecker
}

// NewReadiness returns a readiness gate bound to a DB checker.
func NewReadiness(db ReadyChecker) *Readiness {
	return &Readiness{db: db}
}

// MarkKindsRegistered flips the Control registration gate.
func (r *Readiness) MarkKindsRegistered() {
	r.kindsRegistered.Store(true)
}

// KindsRegistered reports whether kind registration succeeded once.
func (r *Readiness) KindsRegistered() bool {
	return r.kindsRegistered.Load()
}

type healthResponse struct {
	Status string `json:"status"`
}

// HealthHandler serves /health/live and /health/ready.
type HealthHandler struct {
	ready *Readiness
}

// NewHealthHandler returns health handlers.
func NewHealthHandler(ready *Readiness) *HealthHandler {
	return &HealthHandler{ready: ready}
}

// Register mounts live/ready routes on mux.
func (h *HealthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", h.handleLive)
	mux.HandleFunc("GET /health/ready", h.handleReady)
}

func (h *HealthHandler) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *HealthHandler) handleReady(w http.ResponseWriter, r *http.Request) {
	if h.ready == nil || !h.ready.KindsRegistered() {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "not_ready"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if h.ready.db == nil || h.ready.db.Ready(ctx) != nil {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
