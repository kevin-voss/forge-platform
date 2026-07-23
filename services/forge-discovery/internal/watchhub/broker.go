// Package watchhub provides an in-memory fan-out broker for endpoint watch SSE.
package watchhub

import (
	"strconv"
	"sync"
	"sync/atomic"
)

// EventType is the SSE event name for endpoint watch streams.
type EventType string

const (
	EventAdded   EventType = "added"
	EventUpdated EventType = "updated"
	EventRemoved EventType = "removed"
)

// EndpointPayload is the JSON body carried by added/updated (and partial removed) events.
type EndpointPayload struct {
	ID              string  `json:"id"`
	Service         string  `json:"service,omitempty"`
	Node            string  `json:"node,omitempty"`
	Phase           string  `json:"phase,omitempty"`
	Ready           bool    `json:"ready,omitempty"`
	Revision        string  `json:"revision,omitempty"`
	Protocol        string  `json:"protocol,omitempty"`
	ResourceVersion string  `json:"resourceVersion"`
	UnreadyReason   *string `json:"unreadyReason,omitempty"`
	Address         *struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"address,omitempty"`
}

// Event is one watch change for a scoped service.
type Event struct {
	Type            EventType
	Project         string
	Environment     string
	Service         string
	ResourceVersion string
	Payload         EndpointPayload
}

// ServiceKey builds the broker partition key.
func ServiceKey(project, environment, service string) string {
	return project + "/" + environment + "/" + service
}

// ReplayResult is the outcome of a since-cursor lookup.
type ReplayResult struct {
	// Events are retained events with ResourceVersion > Since (ordered).
	Events []Event
	// Miss is true when Since is older than the retained buffer (caller should resync).
	Miss bool
}

// Config sizes the in-memory ring and connection cap.
type Config struct {
	BufferSize     int
	MaxConnections int
}

// Broker fans out endpoint change events to SSE subscribers with per-service ring buffers.
type Broker struct {
	cfg Config

	mu      sync.Mutex
	buffers map[string]*serviceBuffer
	active  atomic.Int64
}

type serviceBuffer struct {
	nextRV int64
	// ring holds events in ascending resourceVersion order (oldest at index 0).
	ring []Event
	subs map[chan Event]struct{}
}

// New creates a Broker with defaults applied.
func New(cfg Config) *Broker {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 500
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 1000
	}
	return &Broker{
		cfg:     cfg,
		buffers: map[string]*serviceBuffer{},
	}
}

// ActiveConnections returns the number of open watch subscriptions.
func (b *Broker) ActiveConnections() int64 {
	return b.active.Load()
}

// MaxConnections returns the configured SSE connection cap.
func (b *Broker) MaxConnections() int {
	return b.cfg.MaxConnections
}

// TryAcquireConnection reserves a watch slot. Returns false when at the cap.
func (b *Broker) TryAcquireConnection() bool {
	for {
		cur := b.active.Load()
		if cur >= int64(b.cfg.MaxConnections) {
			return false
		}
		if b.active.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// ReleaseConnection frees a previously acquired watch slot.
func (b *Broker) ReleaseConnection() {
	b.active.Add(-1)
}

// Publish appends an event to the service ring and notifies live subscribers.
// Assigns a monotonic per-service resourceVersion and returns the published event.
func (b *Broker) Publish(ev Event) Event {
	key := ServiceKey(ev.Project, ev.Environment, ev.Service)
	b.mu.Lock()
	defer b.mu.Unlock()

	buf := b.buffers[key]
	if buf == nil {
		buf = &serviceBuffer{subs: map[chan Event]struct{}{}}
		b.buffers[key] = buf
	}
	buf.nextRV++
	ev.ResourceVersion = strconv.FormatInt(buf.nextRV, 10)
	ev.Payload.ResourceVersion = ev.ResourceVersion
	if len(buf.ring) >= b.cfg.BufferSize {
		buf.ring = buf.ring[1:]
	}
	buf.ring = append(buf.ring, ev)

	for ch := range buf.subs {
		select {
		case ch <- ev:
		default:
			// Slow subscriber: drop; client can reconnect and resync.
		}
	}
	return ev
}

// Replay returns retained events after since, or Miss when the cursor is too old.
// since == 0 with a non-empty buffer replays from the start of the buffer (not a miss).
// since older than the oldest retained version (since+1 < oldest) is a miss.
func (b *Broker) Replay(project, environment, service string, since int64) ReplayResult {
	key := ServiceKey(project, environment, service)
	b.mu.Lock()
	defer b.mu.Unlock()

	buf := b.buffers[key]
	if buf == nil || len(buf.ring) == 0 {
		// Empty buffer: treat as miss so callers can perform a full Ready resync
		// (except when since is 0 and there is truly nothing to sync yet — still Miss
		// so list+added runs and converges to the current Ready set).
		return ReplayResult{Miss: true}
	}
	oldest, _ := strconv.ParseInt(buf.ring[0].ResourceVersion, 10, 64)
	if since >= 0 && since+1 < oldest {
		return ReplayResult{Miss: true}
	}
	out := make([]Event, 0, len(buf.ring))
	for _, ev := range buf.ring {
		rv, _ := strconv.ParseInt(ev.ResourceVersion, 10, 64)
		if rv > since {
			out = append(out, ev)
		}
	}
	return ReplayResult{Events: out, Miss: false}
}

// Subscribe registers a buffered channel for live events. Caller must Unsubscribe.
func (b *Broker) Subscribe(project, environment, service string) chan Event {
	key := ServiceKey(project, environment, service)
	ch := make(chan Event, 32)
	b.mu.Lock()
	defer b.mu.Unlock()
	buf := b.buffers[key]
	if buf == nil {
		buf = &serviceBuffer{subs: map[chan Event]struct{}{}}
		b.buffers[key] = buf
	}
	buf.subs[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a live subscription channel.
func (b *Broker) Unsubscribe(project, environment, service string, ch chan Event) {
	key := ServiceKey(project, environment, service)
	b.mu.Lock()
	defer b.mu.Unlock()
	buf := b.buffers[key]
	if buf == nil {
		return
	}
	delete(buf.subs, ch)
	close(ch)
}

// LatestResourceVersion returns the newest retained version for a service, or 0.
func (b *Broker) LatestResourceVersion(project, environment, service string) int64 {
	key := ServiceKey(project, environment, service)
	b.mu.Lock()
	defer b.mu.Unlock()
	buf := b.buffers[key]
	if buf == nil || len(buf.ring) == 0 {
		return 0
	}
	rv, _ := strconv.ParseInt(buf.ring[len(buf.ring)-1].ResourceVersion, 10, 64)
	return rv
}
