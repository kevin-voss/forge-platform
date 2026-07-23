package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"forge.local/services/forge-events/internal/identity"
	"forge.local/services/forge-events/internal/idempotency"
)

// ProcessedMarker marks and queries per-consumer processed events.
type ProcessedMarker interface {
	Mark(ctx context.Context, consumer, eventID string) error
	IsProcessed(ctx context.Context, consumer, eventID string) (bool, error)
}

// ConsumerAuthorizer looks up a durable and authorizes the caller against its identity.
type ConsumerAuthorizer interface {
	Authorize(ctx context.Context, consumerName string, principal identity.Principal) error
}

type processedBody struct {
	Consumer string `json:"consumer"`
	EventID  string `json:"event_id"`
}

type processedQueryResponse struct {
	Processed bool `json:"processed"`
}

// ProcessedHandler serves POST/GET /v1/processed.
type ProcessedHandler struct {
	Store     ProcessedMarker
	Auth      *identity.Gate
	Authorizer ConsumerAuthorizer
	Metrics   *idempotency.Metrics
	MaxBytes  int
}

// Register mounts processed routes.
func (h *ProcessedHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/processed", h.handleMark)
	mux.HandleFunc("GET /v1/processed", h.handleGet)
}

func (h *ProcessedHandler) handleMark(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authPrincipal(w, r)
	if !ok {
		return
	}
	body, ok := readLimitedJSON(w, r, h.MaxBytes)
	if !ok {
		return
	}
	var req processedBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	req.Consumer = strings.TrimSpace(req.Consumer)
	req.EventID = strings.TrimSpace(req.EventID)
	if req.Consumer == "" || req.EventID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "consumer and event_id are required", nil)
		return
	}
	if !h.authorizeConsumer(w, r, req.Consumer, principal) {
		return
	}
	if h.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "seen store not ready", nil)
		return
	}
	if err := h.Store.Mark(r.Context(), req.Consumer, req.EventID); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	if h.Metrics != nil {
		h.Metrics.ProcessedEvents.Add(1)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ProcessedHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authPrincipal(w, r)
	if !ok {
		return
	}
	consumer := strings.TrimSpace(r.URL.Query().Get("consumer"))
	eventID := strings.TrimSpace(r.URL.Query().Get("event_id"))
	if consumer == "" || eventID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "consumer and event_id query params are required", nil)
		return
	}
	if !h.authorizeConsumer(w, r, consumer, principal) {
		return
	}
	if h.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "seen store not ready", nil)
		return
	}
	processed, err := h.Store.IsProcessed(r.Context(), consumer, eventID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, processedQueryResponse{Processed: processed})
}

func (h *ProcessedHandler) authPrincipal(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.Auth == nil {
		return identity.Principal{}, true
	}
	p, err := h.Auth.Authenticate(r)
	if err != nil {
		writeAuthErr(w, err)
		return identity.Principal{}, false
	}
	return p, true
}

func (h *ProcessedHandler) authorizeConsumer(w http.ResponseWriter, r *http.Request, consumer string, principal identity.Principal) bool {
	if h.Authorizer == nil {
		return true
	}
	if err := h.Authorizer.Authorize(r.Context(), consumer, principal); err != nil {
		writeAuthErr(w, err)
		return false
	}
	return true
}

func writeAuthErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token", nil)
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "consumer identity mismatch", nil)
	default:
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication failed", map[string]string{
			"cause": err.Error(),
		})
	}
}
