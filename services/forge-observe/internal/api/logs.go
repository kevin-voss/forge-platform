package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"forge.local/services/forge-observe/internal/identity"
	"forge.local/services/forge-observe/internal/logs"
)

// LogsHandler serves GET /v1/logs.
type LogsHandler struct {
	Service *logs.Service
	Caps    logs.Caps
	Auth    *identity.Gate
	Log     *slog.Logger
	Now     func() time.Time
}

// Register mounts the log query route.
func (h *LogsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/logs", h.handleQuery)
}

func (h *LogsHandler) handleQuery(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	caps := h.Caps
	if caps.MaxLimit == 0 {
		caps = logs.DefaultCaps()
	}

	q := r.URL.Query()
	f, err := logs.ValidateAndNormalize(
		q.Get("project"),
		q.Get("deployment"),
		q.Get("service"),
		q.Get("request_id"),
		q.Get("trace_id"),
		q.Get("q"),
		q.Get("since"),
		q.Get("until"),
		q.Get("limit"),
		q.Get("direction"),
		q.Get("cursor"),
		now,
		caps,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filters", err.Error(), nil)
		return
	}

	principal, err := h.Auth.Authenticate(r)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication failed", nil)
		return
	}

	allowed, err := h.Auth.AuthorizeProject(r.Context(), principal, f.Project)
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			writeError(w, http.StatusForbidden, "forbidden", "project log access denied", map[string]string{
				"project": f.Project,
			})
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", err.Error(), nil)
		return
	}

	if h.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "loki_unavailable", "log query service not configured", nil)
		return
	}

	result, err := h.Service.Query(r.Context(), f)
	if err != nil {
		if errors.Is(err, logs.ErrLokiUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "loki_unavailable", "Loki is unavailable; try again later", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "query_failed", err.Error(), nil)
		return
	}

	if allowed != nil {
		result.Entries = logs.FilterByProjects(result.Entries, allowed, false)
		// Empty result for unauthorized project is also acceptable; when a
		// specific project was requested and denied we already 403'd above.
	}

	writeJSON(w, http.StatusOK, result)
}
