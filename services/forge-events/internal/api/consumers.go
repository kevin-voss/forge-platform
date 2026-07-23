package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"forge.local/services/forge-events/internal/consumers"
)

// ConsumerStore creates durable consumers.
type ConsumerStore interface {
	Create(req consumers.CreateRequest) (consumers.ConsumerInfo, error)
}

// Acker acknowledges or nacks deliveries by opaque token.
type Acker interface {
	Ack(token string) error
	Nak(token string, delay time.Duration) error
}

type createConsumerBody struct {
	Name          string `json:"name"`
	Subject       string `json:"subject"`
	AckWaitS      int    `json:"ack_wait_s"`
	MaxDeliveries int    `json:"max_deliveries"`
}

type ackBody struct {
	AckToken string `json:"ack_token"`
}

type nakBody struct {
	AckToken string `json:"ack_token"`
	DelayS   *int   `json:"delay_s"`
}

// ConsumersHandler serves POST /v1/consumers, /v1/ack, /v1/nak.
type ConsumersHandler struct {
	Store    ConsumerStore
	Acker    Acker
	MaxBytes int
}

// Register mounts consumer and ack routes.
func (h *ConsumersHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/consumers", h.handleCreate)
	mux.HandleFunc("POST /v1/ack", h.handleAck)
	mux.HandleFunc("POST /v1/nak", h.handleNak)
}

func (h *ConsumersHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readLimitedJSON(w, r, h.MaxBytes)
	if !ok {
		return
	}
	var req createConsumerBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "name is required", nil)
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "subject is required", nil)
		return
	}

	info, err := h.Store.Create(consumers.CreateRequest{
		Name:          req.Name,
		Subject:       req.Subject,
		AckWaitS:      req.AckWaitS,
		MaxDeliveries: req.MaxDeliveries,
	})
	if err != nil {
		switch {
		case errors.Is(err, consumers.ErrInvalidConfig):
			writeError(w, http.StatusBadRequest, "validation_error", err.Error(), nil)
		case errors.Is(err, consumers.ErrConflict):
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
		case errors.Is(err, consumers.ErrNotReady):
			writeError(w, http.StatusServiceUnavailable, "unavailable", "jetstream not ready", nil)
		default:
			writeError(w, http.StatusServiceUnavailable, "unavailable", "create consumer failed", map[string]string{
				"cause": err.Error(),
			})
		}
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (h *ConsumersHandler) handleAck(w http.ResponseWriter, r *http.Request) {
	body, ok := readLimitedJSON(w, r, h.MaxBytes)
	if !ok {
		return
	}
	var req ackBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	if req.AckToken == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "ack_token is required", nil)
		return
	}
	if err := h.Acker.Ack(req.AckToken); err != nil {
		writeAckErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ConsumersHandler) handleNak(w http.ResponseWriter, r *http.Request) {
	body, ok := readLimitedJSON(w, r, h.MaxBytes)
	if !ok {
		return
	}
	var req nakBody
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body", nil)
		return
	}
	if req.AckToken == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "ack_token is required", nil)
		return
	}
	var delay time.Duration
	if req.DelayS != nil {
		if *req.DelayS < 0 {
			writeError(w, http.StatusBadRequest, "validation_error", "delay_s must be >= 0", nil)
			return
		}
		delay = time.Duration(*req.DelayS) * time.Second
	}
	if err := h.Acker.Nak(req.AckToken, delay); err != nil {
		writeAckErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAckErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, consumers.ErrAckNotFound), errors.Is(err, consumers.ErrAckUsed):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, consumers.ErrAckExpired):
		writeError(w, http.StatusGone, "gone", err.Error(), nil)
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable", "ack/nak failed", map[string]string{
			"cause": err.Error(),
		})
	}
}

func readLimitedJSON(w http.ResponseWriter, r *http.Request, max int) ([]byte, bool) {
	if max <= 0 {
		max = 256 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(max)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "unable to read request body", nil)
		return nil, false
	}
	return body, true
}
