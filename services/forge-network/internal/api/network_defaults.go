package api

import (
	"log/slog"
	"net/http"
	"strings"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/policy"
)

// NetworkDefaultsHandler serves per-environment default policy.
type NetworkDefaultsHandler struct {
	Store *policy.Store
	Log   *slog.Logger
}

// Register mounts defaults routes.
func (h *NetworkDefaultsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/projects/{project}/environments/{environment}/network-defaults", h.get)
	mux.HandleFunc("PATCH /v1/projects/{project}/environments/{environment}/network-defaults", h.patch)
}

func (h *NetworkDefaultsHandler) get(w http.ResponseWriter, r *http.Request) {
	d, err := h.Store.GetOrCreateDefaults(r.Context(), orgFromQuery(r),
		r.PathValue("project"), r.PathValue("environment"))
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *NetworkDefaultsHandler) patch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DefaultPolicy string `json:"defaultPolicy"`
		Organization  string `json:"organization"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	org := strings.TrimSpace(req.Organization)
	if org == "" {
		org = orgFromQuery(r)
	}
	d, err := h.Store.PatchDefaults(r.Context(), org, r.PathValue("project"),
		r.PathValue("environment"), req.DefaultPolicy)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}
