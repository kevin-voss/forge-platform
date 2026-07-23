package consumers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

// Sentinel errors for ack/nak token handling.
var (
	ErrAckNotFound = errors.New("ack token not found")
	ErrAckExpired  = errors.New("ack token expired")
	ErrAckUsed     = errors.New("ack token already used")
)

// AckMetrics tracks ack/nak/redelivery counters.
type AckMetrics struct {
	Acked       atomic.Uint64
	Nacked      atomic.Uint64
	Redelivered atomic.Uint64
	Pending     atomic.Int64
}

// TerminalHandler routes a message that has exhausted max_deliveries (DLQ).
// Implementations must never silently drop the payload.
type TerminalHandler func(ctx context.Context, msg *nats.Msg, consumer, eventID string, deliveryCount int, lastError string) error

// pendingDelivery holds an in-flight JetStream message awaiting ack/nak.
type pendingDelivery struct {
	msg           *nats.Msg
	consumer      string
	eventID       string
	deliveryCount int
	maxDeliveries int
	expires       time.Time
	used          bool
}

// AckManager maps opaque single-use ack tokens to JetStream deliveries.
type AckManager struct {
	ttl      time.Duration
	log      *slog.Logger
	metrics  *AckMetrics
	terminal TerminalHandler

	mu      sync.Mutex
	pending map[string]*pendingDelivery
}

// NewAckManager constructs an AckManager. ttl should be >= ack_wait.
func NewAckManager(ttl time.Duration, log *slog.Logger, metrics *AckMetrics) *AckManager {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &AckMetrics{}
	}
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &AckManager{
		ttl:     ttl,
		log:     log,
		metrics: metrics,
		pending: make(map[string]*pendingDelivery),
	}
}

// SetTerminalHandler registers the DLQ (or other) handler for max-delivery exhaustion.
func (a *AckManager) SetTerminalHandler(h TerminalHandler) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terminal = h
}

// Register stores msg under a new opaque token. Caller must not ack the msg.
func (a *AckManager) Register(msg *nats.Msg, consumer, eventID string, deliveryCount int) string {
	return a.RegisterDelivery(msg, consumer, eventID, deliveryCount, 0)
}

// RegisterDelivery stores msg with max_deliveries so Nak can detect the terminal attempt.
func (a *AckManager) RegisterDelivery(msg *nats.Msg, consumer, eventID string, deliveryCount, maxDeliveries int) string {
	token := newAckToken()
	a.mu.Lock()
	a.pending[token] = &pendingDelivery{
		msg:           msg,
		consumer:      consumer,
		eventID:       eventID,
		deliveryCount: deliveryCount,
		maxDeliveries: maxDeliveries,
		expires:       time.Now().Add(a.ttl),
	}
	pending := int64(len(a.pending))
	a.mu.Unlock()
	a.metrics.Pending.Store(pending)
	return token
}

// Ack acknowledges a delivery and advances the durable consumer position.
func (a *AckManager) Ack(token string) error {
	d, err := a.take(token)
	if err != nil {
		return err
	}
	if err := d.msg.Ack(); err != nil {
		return fmt.Errorf("jetstream ack: %w", err)
	}
	a.metrics.Acked.Add(1)
	a.log.Info("event acked",
		"span", "events.ack",
		"consumer", d.consumer,
		"event_id", d.eventID,
		"delivery_count", d.deliveryCount,
	)
	return nil
}

// Nak negatively acknowledges a delivery, optionally delaying redelivery.
// On the final delivery attempt (delivery_count >= max_deliveries), routes to
// the terminal handler (DLQ) and Terms the message instead of nacking.
func (a *AckManager) Nak(token string, delay time.Duration) error {
	d, err := a.take(token)
	if err != nil {
		return err
	}

	a.mu.Lock()
	terminal := a.terminal
	a.mu.Unlock()

	if terminal != nil && d.maxDeliveries > 0 && d.deliveryCount >= d.maxDeliveries {
		lastErr := "max_deliveries exceeded"
		if err := terminal(context.Background(), d.msg, d.consumer, d.eventID, d.deliveryCount, lastErr); err != nil {
			// Handler queues for retry; still Term so the durable stops looping.
			a.log.Error("terminal handler error after max deliveries",
				"span", "events.park",
				"consumer", d.consumer,
				"event_id", d.eventID,
				"delivery_count", d.deliveryCount,
				"error", err.Error(),
			)
		}
		if err := d.msg.Term(); err != nil {
			return fmt.Errorf("jetstream term: %w", err)
		}
		a.metrics.Nacked.Add(1)
		a.log.Warn("event parked after max deliveries (dlq)",
			"span", "events.park",
			"consumer", d.consumer,
			"event_id", d.eventID,
			"delivery_count", d.deliveryCount,
			"max_deliveries", d.maxDeliveries,
		)
		return nil
	}

	if delay > 0 {
		if err := d.msg.NakWithDelay(delay); err != nil {
			return fmt.Errorf("jetstream nak delay: %w", err)
		}
	} else {
		if err := d.msg.Nak(); err != nil {
			return fmt.Errorf("jetstream nak: %w", err)
		}
	}
	a.metrics.Nacked.Add(1)
	a.metrics.Redelivered.Add(1)
	a.log.Info("event nacked",
		"span", "events.redeliver",
		"consumer", d.consumer,
		"event_id", d.eventID,
		"delivery_count", d.deliveryCount,
		"delay_s", int(delay.Seconds()),
	)
	return nil
}

// Pending returns the number of in-flight ack tokens.
func (a *AckManager) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked(time.Now())
	return len(a.pending)
}

func (a *AckManager) take(token string) (*pendingDelivery, error) {
	token = trimToken(token)
	if token == "" {
		return nil, ErrAckNotFound
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked(now)
	d, ok := a.pending[token]
	if !ok {
		return nil, ErrAckNotFound
	}
	if d.used {
		return nil, ErrAckUsed
	}
	if !d.expires.After(now) {
		delete(a.pending, token)
		a.metrics.Pending.Store(int64(len(a.pending)))
		return nil, ErrAckExpired
	}
	d.used = true
	delete(a.pending, token)
	a.metrics.Pending.Store(int64(len(a.pending)))
	return d, nil
}

func (a *AckManager) expireLocked(now time.Time) {
	for tok, d := range a.pending {
		if !d.expires.After(now) {
			delete(a.pending, tok)
		}
	}
	a.metrics.Pending.Store(int64(len(a.pending)))
}

func newAckToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to time-based uniqueness.
		return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("ack_%d", time.Now().UnixNano())))
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func trimToken(token string) string {
	for len(token) > 0 && (token[0] == ' ' || token[0] == '\t' || token[0] == '\n' || token[0] == '\r') {
		token = token[1:]
	}
	for len(token) > 0 {
		last := token[len(token)-1]
		if last == ' ' || last == '\t' || last == '\n' || last == '\r' {
			token = token[:len(token)-1]
			continue
		}
		break
	}
	return token
}
