package api

import (
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// DriftHandler accepts Runtime-reported drift and exposes reconcile triggers.
type DriftHandler struct {
	Metrics *network.DriftMetrics
	Log     *slog.Logger
}

// Register mounts drift routes.
func (h *DriftHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/networks/{name}/route-drift", h.report)
}

type driftReportBody struct {
	DriftCount int64 `json:"drift_count"`
}

func (h *DriftHandler) report(w http.ResponseWriter, r *http.Request) {
	var req driftReportBody
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if h.Metrics != nil {
		h.Metrics.AddRouteDrift(req.DriftCount)
	}
	if h.Log != nil {
		h.Log.Info("route drift reported",
			"event", "network.route.drift_report",
			"network", r.PathValue("name"),
			"drift_count", req.DriftCount,
		)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":      "recorded",
		"drift_count": req.DriftCount,
	})
}
