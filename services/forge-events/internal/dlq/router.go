package dlq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"forge.local/services/forge-events/internal/events"

	"github.com/nats-io/nats.go"
)

// Header keys carrying failure metadata on DLQ messages.
const (
	HeaderDLQID           = "X-Forge-DLQ-Id"
	HeaderOriginalSubject = "X-Forge-Original-Subject"
	HeaderConsumer        = "X-Forge-Consumer"
	HeaderDeliveryCount   = "X-Forge-Delivery-Count"
	HeaderLastError       = "X-Forge-Last-Error"
	HeaderFirstFailedAt   = "X-Forge-First-Failed-At"
	HeaderEventID         = "X-Forge-Event-Id"
	HeaderFamily          = "X-Forge-Family"
)

// Sentinel errors.
var (
	ErrDisabled = errors.New("dlq disabled")
	ErrNotReady = errors.New("jetstream not ready")
)

// JS is the JetStream surface used by Router (for tests).
type JS interface {
	PublishMsg(msg *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error)
}

// Metrics tracks DLQ counters.
type Metrics struct {
	Routed      atomic.Uint64
	Redelivered atomic.Uint64
	Deleted     atomic.Uint64
	Size        atomic.Int64
	RouteFails  atomic.Uint64
}

// TerminalFailure is the input for routing a poison message to the DLQ.
type TerminalFailure struct {
	Payload         []byte
	OriginalSubject string
	Consumer        string
	EventID         string
	DeliveryCount   int
	LastError       string
	FirstFailedAt   time.Time
	Family          string
}

// Router publishes terminal failures to per-family DLQ streams.
type Router struct {
	js      JS
	store   *Store
	enabled bool
	log     *slog.Logger
	metrics *Metrics

	mu     sync.Mutex
	retryQ []TerminalFailure
}

// NewRouter constructs a DLQRouter. store may be nil only in unit tests that
// assert publish side-effects via a mock JS.
func NewRouter(js JS, store *Store, enabled bool, log *slog.Logger, metrics *Metrics) *Router {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &Router{
		js:      js,
		store:   store,
		enabled: enabled,
		log:     log,
		metrics: metrics,
	}
}

// Enabled reports whether DLQ routing is active.
func (r *Router) Enabled() bool {
	return r != nil && r.enabled
}

// Route publishes the failure to dlq.<family> with metadata headers.
// On publish failure the payload is queued for retry — never silently dropped.
func (r *Router) Route(ctx context.Context, fail TerminalFailure) error {
	if r == nil || !r.enabled {
		return ErrDisabled
	}
	if err := r.normalize(&fail); err != nil {
		return err
	}
	if err := r.publish(ctx, fail); err != nil {
		r.metrics.RouteFails.Add(1)
		r.enqueue(fail)
		r.log.Error("dlq route failed; queued for retry",
			"span", "events.dlq.route",
			"event_id", fail.EventID,
			"original_subject", fail.OriginalSubject,
			"consumer", fail.Consumer,
			"delivery_count", fail.DeliveryCount,
			"last_error", fail.LastError,
			"error", err.Error(),
		)
		return fmt.Errorf("dlq publish: %w", err)
	}
	return nil
}

// FlushRetries attempts to publish any queued failures. Safe to call periodically.
func (r *Router) FlushRetries(ctx context.Context) {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	pending := r.retryQ
	r.retryQ = nil
	r.mu.Unlock()
	for _, fail := range pending {
		if err := r.publish(ctx, fail); err != nil {
			r.enqueue(fail)
			r.metrics.RouteFails.Add(1)
			r.log.Warn("dlq retry still failing",
				"span", "events.dlq.route",
				"event_id", fail.EventID,
				"original_subject", fail.OriginalSubject,
				"error", err.Error(),
			)
		}
	}
}

// PendingRetries returns the number of failures waiting for DLQ publish.
func (r *Router) PendingRetries() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.retryQ)
}

func (r *Router) normalize(fail *TerminalFailure) error {
	fail.OriginalSubject = strings.TrimSpace(fail.OriginalSubject)
	fail.Consumer = strings.TrimSpace(fail.Consumer)
	fail.LastError = strings.TrimSpace(fail.LastError)
	if fail.LastError == "" {
		fail.LastError = "max_deliveries exceeded"
	}
	if fail.FirstFailedAt.IsZero() {
		fail.FirstFailedAt = time.Now().UTC().Truncate(time.Millisecond)
	} else {
		fail.FirstFailedAt = fail.FirstFailedAt.UTC().Truncate(time.Millisecond)
	}
	if fail.DeliveryCount < 1 {
		fail.DeliveryCount = 1
	}
	if len(fail.Payload) == 0 {
		return fmt.Errorf("payload is required")
	}
	if fail.OriginalSubject == "" {
		return fmt.Errorf("original_subject is required")
	}
	if fail.Consumer == "" {
		return fmt.Errorf("consumer is required")
	}
	if fail.Family == "" {
		// Infer from subject when families aren't passed through.
		parts := strings.Split(fail.OriginalSubject, ".")
		if len(parts) > 0 {
			fail.Family = parts[0]
		}
	}
	if fail.Family == "" {
		return fmt.Errorf("family is required")
	}
	if fail.EventID == "" {
		if env, err := events.UnmarshalEnvelope(fail.Payload); err == nil {
			fail.EventID = env.ID
		} else {
			fail.EventID = events.NewEventID()
		}
	}
	return nil
}

func (r *Router) publish(_ context.Context, fail TerminalFailure) error {
	if r.js == nil {
		return ErrNotReady
	}
	dlqID := newDLQID()
	subject := DLQSubject(fail.Family)
	msg := &nats.Msg{
		Subject: subject,
		Data:    append([]byte(nil), fail.Payload...),
		Header:  nats.Header{},
	}
	msg.Header.Set(nats.MsgIdHdr, dlqID)
	msg.Header.Set(HeaderDLQID, dlqID)
	msg.Header.Set(HeaderOriginalSubject, fail.OriginalSubject)
	msg.Header.Set(HeaderConsumer, fail.Consumer)
	msg.Header.Set(HeaderDeliveryCount, strconv.Itoa(fail.DeliveryCount))
	msg.Header.Set(HeaderLastError, fail.LastError)
	msg.Header.Set(HeaderFirstFailedAt, fail.FirstFailedAt.Format(time.RFC3339Nano))
	msg.Header.Set(HeaderEventID, fail.EventID)
	msg.Header.Set(HeaderFamily, fail.Family)

	ack, err := r.js.PublishMsg(msg)
	if err != nil {
		return err
	}

	entry := Entry{
		DLQID:           dlqID,
		EventID:         fail.EventID,
		OriginalSubject: fail.OriginalSubject,
		Consumer:        fail.Consumer,
		DeliveryCount:   fail.DeliveryCount,
		LastError:       fail.LastError,
		FirstFailedAt:   fail.FirstFailedAt,
		CreatedAt:       time.Now().UTC().Truncate(time.Millisecond),
		Family:          fail.Family,
		Stream:          ack.Stream,
		Sequence:        ack.Sequence,
		Payload:         json.RawMessage(append([]byte(nil), fail.Payload...)),
	}
	if entry.Stream == "" {
		entry.Stream = DLQStreamName(fail.Family)
	}
	if r.store != nil {
		r.store.Put(entry)
		r.metrics.Size.Store(int64(r.store.Size()))
	}
	r.metrics.Routed.Add(1)
	r.log.Info("event routed to dlq",
		"span", "events.dlq.route",
		"dlq_id", dlqID,
		"event_id", fail.EventID,
		"original_subject", fail.OriginalSubject,
		"consumer", fail.Consumer,
		"delivery_count", fail.DeliveryCount,
		"last_error", fail.LastError,
		"stream", entry.Stream,
		"seq", entry.Sequence,
	)
	return nil
}

func (r *Router) enqueue(fail TerminalFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Bound retry queue to avoid unbounded memory growth.
	const maxRetry = 10_000
	if len(r.retryQ) >= maxRetry {
		r.log.Error("dlq retry queue full; keeping oldest dropped slot occupied",
			"span", "events.dlq.route",
			"event_id", fail.EventID,
			"original_subject", fail.OriginalSubject,
		)
		r.retryQ = r.retryQ[1:]
	}
	r.retryQ = append(r.retryQ, fail)
}

func newDLQID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("dlq_%d", time.Now().UnixNano())
	}
	return "dlq_" + hex.EncodeToString(b[:])
}

// DLQStreamName returns the JetStream stream name for a source family.
func DLQStreamName(family string) string {
	return "dlq_" + family
}

// DLQSubject returns the publish subject for a family's DLQ stream.
func DLQSubject(family string) string {
	return "dlq." + family + ".entry"
}

// DLQSubjectFilter is the stream subject filter for a family.
func DLQSubjectFilter(family string) string {
	return "dlq." + family + ".>"
}
