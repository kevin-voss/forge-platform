package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

// Sentinel errors for publish validation / transport.
var (
	ErrInvalidSubject  = errors.New("invalid subject")
	ErrPayloadTooLarge = errors.New("payload too large")
	ErrNotReady        = errors.New("jetstream not ready")
)

// JSPublisher is the JetStream surface used by Publisher (for tests).
type JSPublisher interface {
	PublishMsg(msg *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error)
}

// PublishRequest is the validated input for a publish call.
type PublishRequest struct {
	Subject string
	Data    json.RawMessage
	Source  string
	Headers map[string]string
}

// PublishResult is returned after a successful JetStream publish.
type PublishResult struct {
	EventID string
	Stream  string
	Seq     uint64
}

// Metrics tracks publish/consume counters for observability.
type Metrics struct {
	Published atomic.Uint64
	Consumed  atomic.Uint64
}

// Publisher validates subjects and publishes envelopes to JetStream.
type Publisher struct {
	js       JSPublisher
	families []string
	maxBytes int
	log      *slog.Logger
	metrics  *Metrics
}

// NewPublisher constructs a Publisher.
func NewPublisher(js JSPublisher, families []string, maxBytes int, log *slog.Logger, metrics *Metrics) *Publisher {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	return &Publisher{
		js:       js,
		families: append([]string(nil), families...),
		maxBytes: maxBytes,
		log:      log,
		metrics:  metrics,
	}
}

// Publish validates, wraps, and stores an event. The envelope id is set as NATS msg-id.
func (p *Publisher) Publish(_ context.Context, req PublishRequest) (PublishResult, error) {
	start := time.Now()
	family, err := FamilyForSubject(req.Subject, p.families)
	if err != nil {
		return PublishResult{}, fmt.Errorf("%w: %v", ErrInvalidSubject, err)
	}
	if req.Data == nil {
		return PublishResult{}, fmt.Errorf("%w: data is required", ErrInvalidSubject)
	}
	if !json.Valid(req.Data) {
		return PublishResult{}, fmt.Errorf("%w: data must be valid JSON", ErrInvalidSubject)
	}
	if len(req.Data) > p.maxBytes {
		return PublishResult{}, fmt.Errorf("%w: data is %d bytes (max %d)", ErrPayloadTooLarge, len(req.Data), p.maxBytes)
	}

	env := NewEnvelope(req.Subject, req.Source, req.Data)
	payload, err := env.Marshal()
	if err != nil {
		return PublishResult{}, fmt.Errorf("marshal envelope: %w", err)
	}
	if len(payload) > p.maxBytes {
		return PublishResult{}, fmt.Errorf("%w: envelope is %d bytes (max %d)", ErrPayloadTooLarge, len(payload), p.maxBytes)
	}

	if p.js == nil {
		return PublishResult{}, ErrNotReady
	}

	msg := &nats.Msg{
		Subject: req.Subject,
		Data:    payload,
		Header:  nats.Header{},
	}
	msg.Header.Set(nats.MsgIdHdr, env.ID)
	for k, v := range req.Headers {
		if k == "" || k == nats.MsgIdHdr {
			continue
		}
		msg.Header.Set(k, v)
	}

	ack, err := p.js.PublishMsg(msg)
	if err != nil {
		return PublishResult{}, fmt.Errorf("jetstream publish: %w", err)
	}

	p.metrics.Published.Add(1)
	p.log.Info("event published",
		"span", "events.publish",
		"event_id", env.ID,
		"subject", req.Subject,
		"stream", ack.Stream,
		"seq", ack.Sequence,
		"bytes", len(payload),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	stream := ack.Stream
	if stream == "" {
		stream = family
	}
	return PublishResult{
		EventID: env.ID,
		Stream:  stream,
		Seq:     ack.Sequence,
	}, nil
}
