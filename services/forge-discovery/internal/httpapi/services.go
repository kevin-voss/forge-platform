package httpapi

import (
	"context"
	"net/http"

	"forge.local/services/forge-discovery/internal/store"
)

// ServiceLister lists registered Service rows (for Gateway discovery sync).
type ServiceLister interface {
	ListServices(ctx context.Context) ([]store.ServiceRow, error)
}

type serviceListItem struct {
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
}

func (h *EndpointsHandler) handleListServices(w http.ResponseWriter, r *http.Request) {
	lister, ok := h.Store.(ServiceLister)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "service listing unavailable")
		return
	}
	rows, err := lister.ListServices(r.Context())
	if err != nil {
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusServiceUnavailable, "list services unavailable: "+err.Error())
		return
	}
	out := make([]serviceListItem, 0, len(rows))
	for _, row := range rows {
		aliases := row.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		out = append(out, serviceListItem{
			Project:     row.Project,
			Environment: row.Environment,
			Name:        row.Name,
			Aliases:     aliases,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
