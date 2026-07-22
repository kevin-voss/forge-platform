package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/proxy"
	"forge.local/services/forge-gateway/internal/routes"
)

// RoutesHandler serves GET/PUT /admin/routes against an in-memory table.
type RoutesHandler struct {
	table   *routes.Table
	proxy   *proxy.Handler
	log     *slog.Logger
	maxBody int64
}

// NewRoutesHandler returns admin route handlers.
func NewRoutesHandler(table *routes.Table, proxyHandler *proxy.Handler, log *slog.Logger) *RoutesHandler {
	if log == nil {
		log = slog.Default()
	}
	return &RoutesHandler{
		table:   table,
		proxy:   proxyHandler,
		log:     log,
		maxBody: 1 << 20, // 1 MiB
	}
}

// Register mounts admin route endpoints on mux.
func (h *RoutesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/routes", h.handleGet)
	mux.HandleFunc("PUT /admin/routes", h.handlePut)
}

func (h *RoutesHandler) handleGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.table.Snapshot())
}

func (h *RoutesHandler) handlePut(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, h.maxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "failed to read body")
		return
	}
	if int64(len(data)) > h.maxBody {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "request body too large")
		return
	}

	var body []routes.Route
	if err := json.Unmarshal(data, &body); err != nil {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "invalid JSON body; expected route array")
		return
	}
	if body == nil {
		body = []routes.Route{}
	}

	if err := h.table.Replace(body); err != nil {
		httperr.WriteDetails(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if h.proxy != nil {
		h.proxy.InvalidatePickers()
	}
	h.log.Info("route table replaced", "route_count", h.table.Len())
	writeJSON(w, http.StatusOK, h.table.Snapshot())
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
