package health

import (
	"encoding/json"
	"net/http"
	"time"
)

// ReadyChecker reports whether the service can accept traffic.
type ReadyChecker interface {
	ReadyError() error
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

// Handler serves /health/live, /health/ready, and identity /.
type Handler struct {
	ready       ReadyChecker
	serviceName string
	version     string
	startedAt   time.Time
}

// NewHandler returns health and identity handlers.
func NewHandler(ready ReadyChecker, serviceName, version string) *Handler {
	return &Handler{
		ready:       ready,
		serviceName: serviceName,
		version:     version,
		startedAt:   time.Now().UTC(),
	}
}

// Register mounts live/ready/identity routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", h.handleLive)
	mux.HandleFunc("GET /health/ready", h.handleReady)
	mux.HandleFunc("GET /{$}", h.handleIdentity)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
