package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// DockerPinger verifies Docker Engine reachability for readiness.
type DockerPinger interface {
	Ping(ctx context.Context) error
}

type response struct {
	Status string `json:"status"`
}

// Handler serves /health/live and /health/ready.
type Handler struct {
	docker DockerPinger
}

// NewHandler returns health handlers that ping Docker for readiness.
func NewHandler(docker DockerPinger) *Handler {
	return &Handler{docker: docker}
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
	if h.docker == nil {
		writeJSON(w, http.StatusServiceUnavailable, response{Status: "not_ready"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.docker.Ping(ctx); err != nil {
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
