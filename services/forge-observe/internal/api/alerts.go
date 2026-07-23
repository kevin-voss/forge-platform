package api

import (
	"errors"
	"log/slog"
	"net/http"

	"forge.local/services/forge-observe/internal/alerts"
	"forge.local/services/forge-observe/internal/identity"
)

// AlertsHandler serves GET /v1/alerts.
type AlertsHandler struct {
	Client *alerts.StatusClient
	Auth   *identity.Gate
	Log    *slog.Logger
}

// Register mounts the alert status route.
func (h *AlertsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/alerts", h.handleList)
}

func (h *AlertsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	principal, err := h.Auth.Authenticate(r)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication failed", nil)
		return
	}

	if h.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "alerting_unavailable", "alerting backend not configured", nil)
		return
	}

	list, err := h.Client.List(r.Context())
	if err != nil {
		if errors.Is(err, alerts.ErrUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "alerting_unavailable", "alerting backend is unavailable; try again later", nil)
			return
		}
		writeError(w, http.StatusBadGateway, "alerting_error", err.Error(), nil)
		return
	}

	allowed, err := h.Auth.AuthorizeProject(r.Context(), principal, "")
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			writeError(w, http.StatusForbidden, "forbidden", "alert access denied", nil)
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", err.Error(), nil)
		return
	}
	if allowed != nil {
		list = alerts.FilterByProjects(list, allowed)
	}

	if h.Log != nil {
		firing := 0
		for _, a := range list {
			if a.State == "firing" {
				firing++
			}
		}
		h.Log.Info("alert status listed",
			"span", "observe.alerts.list",
			"forge_alerts_firing", firing,
			"alert_count", len(list),
		)
	}

	writeJSON(w, http.StatusOK, list)
}
