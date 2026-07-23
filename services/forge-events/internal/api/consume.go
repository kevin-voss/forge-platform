package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"forge.local/services/forge-events/internal/events"
)

// PullConsumer fetches event batches from JetStream.
type PullConsumer interface {
	Consume(ctx context.Context, req events.ConsumeRequest) (events.ConsumeResult, error)
}

type consumeRequestBody struct {
	Subject  string `json:"subject"`
	Batch    int    `json:"batch"`
	Consumer string `json:"consumer"`
}

// ConsumeHandler serves POST /v1/consume.
type ConsumeHandler struct {
	Consumer PullConsumer
	MaxBytes int
	Wait     time.Duration
}

// Register mounts consume routes.
func (h *ConsumeHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/consume", h.handleConsume)
}

func (h *ConsumeHandler) handleConsume(w http.ResponseWriter, r *http.Request) {
	max := h.MaxBytes
	if max <= 0 {
		max = 256 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(max)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "unable to read request body", nil)
		return
	}
	var req consumeRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "subject is required", nil)
		return
	}

	wait := h.Wait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	// Bound request slightly above pull wait so empty batches can return cleanly.
	ctx, cancel := context.WithTimeout(r.Context(), wait+2*time.Second)
	defer cancel()

	result, err := h.Consumer.Consume(ctx, events.ConsumeRequest{
		Subject:  req.Subject,
		Batch:    req.Batch,
		Consumer: req.Consumer,
	})
	if err != nil {
		switch {
		case errors.Is(err, events.ErrInvalidSubject):
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		case errors.Is(err, events.ErrNotReady):
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
