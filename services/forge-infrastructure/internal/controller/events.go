package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PhaseChangedEvent is the platform envelope for resource.node.phasechanged.
type PhaseChangedEvent struct {
	EventID            string `json:"event_id"`
	Type               string `json:"type"`
	ResourceID         string `json:"resource_id"`
	ResourceGeneration int64  `json:"resource_generation"`
	Generation         int64  `json:"generation"`
	From               string `json:"from"`
	To                 string `json:"to"`
	OccurredAt         string `json:"occurred_at"`
	Timestamp          string `json:"timestamp"`
	Producer           string `json:"producer"`
	SchemaVersion      string `json:"schema_version"`
	TraceID            string `json:"trace_id"`
	Reason             string `json:"reason,omitempty"`
}

// EventPublisher emits phase-changed events.
type EventPublisher interface {
	PublishPhaseChanged(ctx context.Context, ev PhaseChangedEvent) error
}

// MemoryEvents collects events for tests.
type MemoryEvents struct {
	mu     sync.Mutex
	Events []PhaseChangedEvent
}

func (m *MemoryEvents) PublishPhaseChanged(ctx context.Context, ev PhaseChangedEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, ev)
	return nil
}

// HTTPEvents posts to forge-events POST /v1/events (best-effort).
type HTTPEvents struct {
	BaseURL    string
	HTTPClient *http.Client
	Source     string
}

func (h *HTTPEvents) PublishPhaseChanged(ctx context.Context, ev PhaseChangedEvent) error {
	if h == nil || strings.TrimSpace(h.BaseURL) == "" {
		return nil
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"subject":  ev.Type,
		"data":     json.RawMessage(data),
		"source":   h.Source,
		"event_id": ev.EventID,
		"headers": map[string]string{
			"schema_version": ev.SchemaVersion,
			"trace_id":       ev.TraceID,
		},
	})
	if err != nil {
		return err
	}
	client := h.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.BaseURL, "/")+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", ev.EventID)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("publish event: status %d", resp.StatusCode)
	}
	return nil
}

// NewPhaseChangedEvent builds a fully-populated envelope.
func NewPhaseChangedEvent(resourceID string, generation int64, from, to, reason, traceID string, at time.Time) PhaseChangedEvent {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	ts := at.UTC().Format(time.RFC3339)
	if traceID == "" {
		traceID = newTraceID()
	}
	return PhaseChangedEvent{
		EventID:            "evt_" + newULIDLike(),
		Type:               "resource.node.phasechanged",
		ResourceID:         resourceID,
		ResourceGeneration: generation,
		Generation:         generation,
		From:               from,
		To:                 to,
		OccurredAt:         ts,
		Timestamp:          ts,
		Producer:           "forge-infrastructure",
		SchemaVersion:      "1",
		TraceID:            traceID,
		Reason:             reason,
	}
}

func newTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newULIDLike() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return strings.ToUpper(hex.EncodeToString(b[:]))
}
