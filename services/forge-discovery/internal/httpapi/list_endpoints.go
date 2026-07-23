package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"forge.local/services/forge-discovery/internal/store"
	"go.opentelemetry.io/otel/attribute"
)

// ListEndpointStore is the persistence surface for readiness-filtered lists.
type ListEndpointStore interface {
	ListEndpoints(ctx context.Context, f store.ListFilter) ([]store.EndpointRow, error)
}

func (h *EndpointsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer().Start(r.Context(), "discovery.endpoints.list")
	defer span.End()

	project := r.PathValue("project")
	environment := r.PathValue("environment")
	service := r.PathValue("service")
	if project == "" || environment == "" || service == "" {
		writeErr(w, http.StatusBadRequest, "project, environment, and service are required")
		return
	}

	readyOnly := true
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("ready"))) {
	case "false", "0", "no":
		readyOnly = false
	}
	revision := strings.TrimSpace(r.URL.Query().Get("revision"))

	span.SetAttributes(
		attribute.String("service", service),
		attribute.Bool("ready_only", readyOnly),
		attribute.String("revision", revision),
	)

	listStore, ok := h.Store.(ListEndpointStore)
	if !ok {
		// Fallback for test fakes that only implement ListServiceEndpoints.
		rows, err := h.Store.ListServiceEndpoints(ctx, project, environment, service)
		if err != nil {
			writeListUnavailable(w, err)
			return
		}
		if readyOnly || revision != "" {
			filtered := make([]store.EndpointRow, 0, len(rows))
			for _, row := range rows {
				if readyOnly && row.Phase != "Ready" {
					continue
				}
				if revision != "" && row.Revision != revision {
					continue
				}
				filtered = append(filtered, row)
			}
			rows = filtered
		}
		h.incListMetric(service)
		writeJSON(w, http.StatusOK, toListItems(rows))
		return
	}

	rows, err := listStore.ListEndpoints(ctx, store.ListFilter{
		Project: project, Environment: environment, Service: service,
		ReadyOnly: readyOnly, Revision: revision,
	})
	if err != nil {
		writeListUnavailable(w, err)
		return
	}
	h.incListMetric(service)
	writeJSON(w, http.StatusOK, toListItems(rows))
}

func toListItems(rows []store.EndpointRow) []endpointListItem {
	out := make([]endpointListItem, 0, len(rows))
	for _, row := range rows {
		item := endpointListItem{
			ID:              row.ID,
			Service:         row.Service,
			Node:            row.NodeID,
			Phase:           row.Phase,
			Ready:           row.Ready,
			ExpiresAt:       row.ExpiresAt.UTC().Format(time.RFC3339),
			UnreadyReason:   row.UnreadyReason,
			Protocol:        row.Protocol,
			Revision:        row.Revision,
			ResourceVersion: row.ResourceVersion,
		}
		item.Address.IP = row.AddressIP
		item.Address.Port = row.AddressPort
		out = append(out, item)
	}
	return out
}

func writeListUnavailable(w http.ResponseWriter, err error) {
	w.Header().Set("Retry-After", "1")
	writeErr(w, http.StatusServiceUnavailable, "list unavailable: "+err.Error())
}

func (h *EndpointsHandler) incListMetric(service string) {
	if h.Metrics != nil {
		h.Metrics.IncListRequests(service)
	}
}
