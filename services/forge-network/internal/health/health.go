package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// ReadyChecker reports whether the service can serve traffic.
type ReadyChecker interface {
	Ready(ctx context.Context) error
}

type healthResponse struct {
	Status string `json:"status"`
}

// Handler serves /health/live and /health/ready.
type Handler struct {
	DB ReadyChecker
}

// Register mounts live/ready routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", h.handleLive)
	mux.HandleFunc("GET /health/ready", h.handleReady)
}

func (h *Handler) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if h.DB == nil || h.DB.Ready(ctx) != nil {
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
