package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"forge.local/services/forge-events/internal/consumers"
	"forge.local/services/forge-events/internal/events"
	"forge.local/services/forge-events/internal/identity"
)

// PullConsumer fetches event batches from a named durable consumer.
type PullConsumer interface {
	Consume(ctx context.Context, req consumers.ConsumeRequest) (events.ConsumeResult, error)
}

type consumeRequestBody struct {
	Consumer string `json:"consumer"`
	Batch    int    `json:"batch"`
}

// ConsumeHandler serves POST /v1/consume.
type ConsumeHandler struct {
	Consumer   PullConsumer
	Auth       *identity.Gate
	Authorizer ConsumerAuthorizer
	MaxBytes   int
	Wait       time.Duration
}

// Register mounts consume routes.
func (h *ConsumeHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/consume", h.handleConsume)
}

func (h *ConsumeHandler) handleConsume(w http.ResponseWriter, r *http.Request) {
	var principal identity.Principal
	if h.Auth != nil {
		p, err := h.Auth.Authenticate(r)
		if err != nil {
			writeAuthErr(w, err)
			return
		}
		principal = p
	}
	body, ok := readLimitedJSON(w, r, h.MaxBytes)
	if !ok {
		return
	}
	var req consumeRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	if req.Consumer == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "consumer is required", nil)
		return
	}
	if h.Authorizer != nil {
		if err := h.Authorizer.Authorize(r.Context(), req.Consumer, principal); err != nil {
			writeAuthErr(w, err)
			return
		}
	}

	wait := h.Wait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	// Bound request slightly above pull wait so empty batches can return cleanly.
	ctx, cancel := context.WithTimeout(r.Context(), wait+2*time.Second)
	defer cancel()

	result, err := h.Consumer.Consume(ctx, consumers.ConsumeRequest{
		Consumer: req.Consumer,
		Batch:    req.Batch,
	})
	if err != nil {
		switch {
		case errors.Is(err, consumers.ErrInvalidConfig):
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		case errors.Is(err, consumers.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		case errors.Is(err, consumers.ErrNotReady):
			writeError(w, http.StatusServiceUnavailable, "unavailable", "jetstream not ready", nil)
		default:
			writeError(w, http.StatusServiceUnavailable, "unavailable", "consume failed", map[string]string{
				"cause": err.Error(),
			})
		}
		return
	}
	if result.Messages == nil {
		result.Messages = []events.DeliveredMessage{}
	}
	writeJSON(w, http.StatusOK, result)
}
