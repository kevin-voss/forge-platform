package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"forge.local/services/forge-events/internal/dlq"
)

// DLQLister lists and inspects DLQ entries.
type DLQLister interface {
	List(f dlq.ListFilter) []dlq.Entry
	Detail(id string) (dlq.Detail, error)
	Delete(id string) error
}

// DLQRedeliverer republishes a DLQ entry to its original subject.
type DLQRedeliverer interface {
	Redeliver(ctx context.Context, dlqID string) (dlq.RedeliverResult, error)
}

// DLQHandler serves GET/DELETE /v1/dlq and POST /v1/dlq/{id}:redeliver.
type DLQHandler struct {
	Store       DLQLister
	Redeliverer DLQRedeliverer
	Enabled     bool
}

// Register mounts DLQ routes.
func (h *DLQHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/dlq", h.handleList)
	mux.HandleFunc("GET /v1/dlq/{id}", h.handleGet)
	mux.HandleFunc("DELETE /v1/dlq/{id}", h.handleDelete)
	mux.HandleFunc("POST /v1/dlq/{id}", h.handlePost)
}

func (h *DLQHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled {
		writeJSON(w, http.StatusOK, []dlq.Entry{})
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusOK, []dlq.Entry{})
		return
	}
	q := r.URL.Query()
	items := h.Store.List(dlq.ListFilter{
		Subject:  q.Get("subject"),
		Consumer: q.Get("consumer"),
	})
	if items == nil {
		items = []dlq.Entry{}
	}
	// List response omits internal/envelope fields via Entry JSON tags.
	writeJSON(w, http.StatusOK, items)
}

func (h *DLQHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled || h.Store == nil {
		writeError(w, http.StatusNotFound, "not_found", "dlq entry not found", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "dlq id is required", nil)
		return
	}
	detail, err := h.Store.Detail(id)
	if err != nil {
		if errors.Is(err, dlq.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "dlq entry not found", nil)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "unavailable", "dlq get failed", map[string]string{
			"cause": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *DLQHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled || h.Store == nil {
		writeError(w, http.StatusNotFound, "not_found", "dlq entry not found", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "dlq id is required", nil)
		return
	}
	if err := h.Store.Delete(id); err != nil {
		if errors.Is(err, dlq.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "dlq entry not found", nil)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "unavailable", "dlq delete failed", map[string]string{
			"cause": err.Error(),
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DLQHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !strings.HasSuffix(id, ":redeliver") {
		writeError(w, http.StatusNotFound, "not_found", "unknown dlq action", nil)
		return
	}
	dlqID := strings.TrimSuffix(id, ":redeliver")
	if dlqID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "dlq id is required", nil)
		return
	}
	if !h.Enabled || h.Redeliverer == nil {
		writeError(w, http.StatusNotFound, "not_found", "dlq entry not found", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := h.Redeliverer.Redeliver(ctx, dlqID)
	if err != nil {
		switch {
		case errors.Is(err, dlq.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "dlq entry not found", nil)
		case errors.Is(err, dlq.ErrNotReady):
			writeError(w, http.StatusServiceUnavailable, "unavailable", "redeliver failed", map[string]string{
				"cause": err.Error(),
			})
		default:
			writeError(w, http.StatusServiceUnavailable, "unavailable", "redeliver failed", map[string]string{
				"cause": err.Error(),
			})
		}
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
