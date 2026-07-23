package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EventType is the SSE / durable watch event name (epic-20 enums).
type EventType string

const (
	EventAdded          EventType = "ADDED"
	EventModified       EventType = "MODIFIED"
	EventStatusModified EventType = "STATUS_MODIFIED"
	EventDeleted        EventType = "DELETED"
)

// WatchEvent is one ScalingPolicy change notification.
type WatchEvent struct {
	Type            EventType `json:"type"`
	ResourceVersion string    `json:"resourceVersion"`
	Resource        Envelope  `json:"resource"`
}

// Hub fans out watch events to SSE subscribers and persists them for replay.
type Hub struct {
	pool *pgxpool.Pool

	mu   sync.Mutex
	subs map[chan WatchEvent]struct{}

	active atomic.Int64
	max    int
}

// NewHub creates a watch hub backed by the given pool.
func NewHub(pool *pgxpool.Pool, maxConnections int) *Hub {
	if maxConnections <= 0 {
		maxConnections = 1000
	}
	return &Hub{
		pool: pool,
		subs: map[chan WatchEvent]struct{}{},
		max:  maxConnections,
	}
}

// TryAcquireConnection reserves a watch slot.
func (h *Hub) TryAcquireConnection() bool {
	for {
		cur := h.active.Load()
		if cur >= int64(h.max) {
			return false
		}
		if h.active.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// ReleaseConnection frees a previously acquired watch slot.
func (h *Hub) ReleaseConnection() {
	h.active.Add(-1)
}

// Publish persists and fans out an event at the given resource version.
func (h *Hub) Publish(ctx context.Context, eventType EventType, env Envelope) error {
	rv, err := ParseRV(env.Metadata.ResourceVersion)
	if err != nil {
		return fmt.Errorf("parse resourceVersion: %w", err)
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = h.pool.Exec(ctx, `
INSERT INTO scaling_policy_events (resource_version, event_type, project, environment, name, payload)
VALUES ($1, $2, $3, $4, $5, $6::jsonb)
ON CONFLICT (resource_version) DO NOTHING`,
		rv, string(eventType), env.Metadata.Project, env.Metadata.Environment, env.Metadata.Name, string(payload),
	)
	if err != nil {
		return err
	}

	ev := WatchEvent{
		Type:            eventType,
		ResourceVersion: env.Metadata.ResourceVersion,
		Resource:        env,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	return nil
}

// Replay returns durable events with resource_version > since.
func (h *Hub) Replay(ctx context.Context, since int64) ([]WatchEvent, error) {
	rows, err := h.pool.Query(ctx, `
SELECT resource_version, event_type, payload
FROM scaling_policy_events
WHERE resource_version > $1
ORDER BY resource_version ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WatchEvent
	for rows.Next() {
		var rv int64
		var typ, payload string
		if err := rows.Scan(&rv, &typ, &payload); err != nil {
			return nil, err
		}
		var env Envelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			return nil, err
		}
		out = append(out, WatchEvent{
			Type:            EventType(typ),
			ResourceVersion: FormatRV(rv),
			Resource:        env,
		})
	}
	return out, rows.Err()
}

// Subscribe registers a buffered channel for live events.
func (h *Hub) Subscribe() chan WatchEvent {
	ch := make(chan WatchEvent, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a live subscription channel.
func (h *Hub) Unsubscribe(ch chan WatchEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}
