package api

import (
	"errors"
	"net/http"

	"forge.local/services/forge-events/internal/schema"
)

// SchemaRegistry lists and describes registered event schemas.
type SchemaRegistry interface {
	List() map[string]schema.SubjectInfo
	Get(subject string) (schema.SubjectDetail, error)
}

// SchemasHandler serves GET /v1/schemas and GET /v1/schemas/{subject}.
type SchemasHandler struct {
	Registry SchemaRegistry
}

// Register mounts schema listing routes.
func (h *SchemasHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/schemas", h.handleList)
	mux.HandleFunc("GET /v1/schemas/{subject}", h.handleGet)
}

func (h *SchemasHandler) handleList(w http.ResponseWriter, _ *http.Request) {
	if h.Registry == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "schemas not loaded", nil)
		return
	}
	writeJSON(w, http.StatusOK, h.Registry.List())
}

func (h *SchemasHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	if h.Registry == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "schemas not loaded", nil)
		return
	}
	subject := r.PathValue("subject")
	if subject == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "subject is required", nil)
		return
	}
	detail, err := h.Registry.Get(subject)
	if err != nil {
		if errors.Is(err, schema.ErrUnknownSchema) {
			writeError(w, http.StatusNotFound, "not_found", "unknown schema subject", map[string]string{
				"subject": subject,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "schema lookup failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}
