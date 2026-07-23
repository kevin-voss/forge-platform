package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"forge.local/services/forge-observe/internal/backends"
)

// ReadyChecker reports whether the service can accept traffic.
type ReadyChecker interface {
	ReadyError() error
}

// BackendStatusProvider returns loki/tempo/prometheus reachability.
type BackendStatusProvider interface {
	StatusMap(ctx context.Context) map[string]string
}

type healthResponse struct {
	Status string `json:"status"`
}

type identityResponse struct {
	Service       string  `json:"service"`
	Language      string  `json:"language"`
	Status        string  `json:"status"`
	Version       string  `json:"version,omitempty"`
	UptimeSeconds float64 `json:"uptime_seconds,omitempty"`
}

// Handler serves /health/live, /health/ready, identity /, and /v1/health/backends.
type Handler struct {
	ready       ReadyChecker
	backends    BackendStatusProvider
	serviceName string
	version     string
	startedAt   time.Time
}

// NewHandler returns health and identity handlers.
func NewHandler(ready ReadyChecker, backends BackendStatusProvider, serviceName, version string) *Handler {
	return &Handler{
		ready:       ready,
		backends:    backends,
		serviceName: serviceName,
		version:     version,
		startedAt:   time.Now().UTC(),
	}
}

// Register mounts live/ready/identity/backends routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", h.handleLive)
	mux.HandleFunc("GET /health/ready", h.handleReady)
	mux.HandleFunc("GET /{$}", h.handleIdentity)
	mux.HandleFunc("GET /v1/health/backends", h.handleBackends)
}

func (h *Handler) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	if h.ready == nil {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "not_ready"})
		return
	}
	if err := h.ready.ReadyError(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *Handler) handleIdentity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, identityResponse{
		Service:       h.serviceName,
		Language:      "go",
		Status:        "running",
		Version:       h.version,
		UptimeSeconds: time.Since(h.startedAt).Seconds(),
	})
}

func (h *Handler) handleBackends(w http.ResponseWriter, r *http.Request) {
	if h.backends == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"loki":       backends.StatusDown,
			"tempo":      backends.StatusDown,
			"prometheus": backends.StatusDown,
		})
		return
	}
	writeJSON(w, http.StatusOK, h.backends.StatusMap(r.Context()))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
