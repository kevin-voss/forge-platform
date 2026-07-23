package audit

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

// Event is a platform autoscaling audit event.
type Event struct {
	EventID   string         `json:"event_id"`
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Policy    string         `json:"policy"`
	Project   string         `json:"project"`
	Env       string         `json:"environment"`
	Payload   map[string]any `json:"payload"`
}

// Publisher emits autoscaling audit events (best-effort).
type Publisher interface {
	Publish(ctx context.Context, ev Event) error
}

// Memory collects events for tests.
type Memory struct {
	mu     sync.Mutex
	Events []Event
}

// Publish appends to the in-memory log.
func (m *Memory) Publish(_ context.Context, ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, ev)
	return nil
}

// Snapshot returns a copy of recorded events.
func (m *Memory) Snapshot() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.Events))
	copy(out, m.Events)
	return out
}

// HTTPPublisher posts to forge-events POST /v1/events.
type HTTPPublisher struct {
	BaseURL    string
	HTTPClient *http.Client
	Source     string
}

// Publish sends the event; empty BaseURL is a no-op.
func (h *HTTPPublisher) Publish(ctx context.Context, ev Event) error {
	if h == nil || strings.TrimSpace(h.BaseURL) == "" {
		return nil
	}
	source := h.Source
	if source == "" {
		source = "forge-autoscaler"
	}
	body, err := json.Marshal(map[string]any{
		"subject":  ev.Type,
		"data":     ev,
		"source":   source,
		"event_id": ev.EventID,
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

// NewEvent builds a fully-populated event envelope.
func NewEvent(eventType, project, env, policy string, payload map[string]any, at time.Time) Event {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return Event{
		EventID:   "evt_" + newID(),
		Type:      eventType,
		Timestamp: at.UTC().Format(time.RFC3339),
		Policy:    policy,
		Project:   project,
		Env:       env,
		Payload:   payload,
	}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

const (
	OverrideCreated = "autoscaling.override.created"
	OverrideExpired = "autoscaling.override.expired"
	OverrideCleared = "autoscaling.override.cleared"
	ScheduleActive  = "autoscaling.schedule.activated"
)
