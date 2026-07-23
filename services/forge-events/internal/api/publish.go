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

// Publisher publishes validated events to JetStream.
type Publisher interface {
	Publish(ctx context.Context, req events.PublishRequest) (events.PublishResult, error)
}

type publishRequestBody struct {
	Subject string            `json:"subject"`
	Data    json.RawMessage   `json:"data"`
	Source  string            `json:"source"`
	Headers map[string]string `json:"headers"`
}

type publishResponseBody struct {
	EventID string `json:"event_id"`
	Stream  string `json:"stream"`
	Seq     uint64 `json:"seq"`
}

// PublishHandler serves POST /v1/events.
type PublishHandler struct {
	Publisher Publisher
	MaxBytes  int
}

// Register mounts publish routes.
func (h *PublishHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/events", h.handlePublish)
}

func (h *PublishHandler) handlePublish(w http.ResponseWriter, r *http.Request) {
	max := h.MaxBytes
	if max <= 0 {
		max = 256 * 1024
	}
	// Allow envelope overhead beyond raw data max.
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(max)+4096))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "unable to read request body", nil)
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "request body is required", nil)
		return
	}
	var req publishRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "subject is required", nil)
		return
	}
	if req.Data == nil {
		writeError(w, http.StatusBadRequest, "validation_error", "data is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	result, err := h.Publisher.Publish(ctx, events.PublishRequest{
		Subject: req.Subject,
		Data:    req.Data,
		Source:  req.Source,
		Headers: req.Headers,
	})
	if err != nil {
		switch {
		case errors.Is(err, events.ErrInvalidSubject):
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		case errors.Is(err, events.ErrPayloadTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", err.Error(), nil)
		case errors.Is(err, events.ErrNotReady):
			writeError(w, http.StatusServiceUnavailable, "unavailable", "jetstream not ready", nil)
		default:
			writeError(w, http.StatusServiceUnavailable, "unavailable", "publish failed", map[string]string{
				"cause": err.Error(),
			})
		}
		return
	}

	writeJSON(w, http.StatusAccepted, publishResponseBody{
		EventID: result.EventID,
		Stream:  result.Stream,
		Seq:     result.Seq,
	})
}
