package events

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nats-io/nats.go"
)

// JSPuller creates pull subscriptions (for tests).
type JSPuller interface {
	PullSubscribe(subj, durable string, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// ConsumeRequest is the input for a pull consume call.
type ConsumeRequest struct {
	Subject  string
	Batch    int
	Consumer string
}

// DeliveredMessage is one message returned to an HTTP consumer.
type DeliveredMessage struct {
	EventID       string          `json:"event_id"`
	Subject       string          `json:"subject"`
	Time          time.Time       `json:"time"`
	Source        string          `json:"source,omitempty"`
	Data          json.RawMessage `json:"data"`
	AckToken      string          `json:"ack_token"`
	DeliveryCount int             `json:"delivery_count"`
}

// ConsumeResult is a batch of delivered messages.
type ConsumeResult struct {
	Messages []DeliveredMessage `json:"messages"`
}

// Consumer fetches batches from JetStream via named pull consumers.
// Messages are auto-acked on deliver (explicit ack API arrives in 11.03).
type Consumer struct {
	js       JSPuller
	families []string
	maxBatch int
	wait     time.Duration
	log      *slog.Logger
	metrics  *Metrics

	mu   sync.Mutex
	subs map[string]*nats.Subscription
}

// NewConsumer constructs a pull Consumer.
func NewConsumer(js JSPuller, families []string, maxBatch int, wait time.Duration, log *slog.Logger, metrics *Metrics) *Consumer {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	if maxBatch <= 0 {
		maxBatch = 100
	}
	if wait <= 0 {
		wait = 2 * time.Second
	}
	return &Consumer{
		js:       js,
		families: append([]string(nil), families...),
		maxBatch: maxBatch,
		wait:     wait,
		log:      log,
		metrics:  metrics,
		subs:     make(map[string]*nats.Subscription),
	}
}

// Consume pull-fetches up to batch messages for subject, auto-acking each.
func (c *Consumer) Consume(ctx context.Context, req ConsumeRequest) (ConsumeResult, error) {
	start := time.Now()
	family, err := FamilyForSubject(req.Subject, c.families)
	if err != nil {
		return ConsumeResult{}, fmt.Errorf("%w: %v", ErrInvalidSubject, err)
	}
	batch := req.Batch
	if batch <= 0 {
		batch = 1
	}
	if batch > c.maxBatch {
		batch = c.maxBatch
	}

	if c.js == nil {
		return ConsumeResult{}, ErrNotReady
	}

	durable := strings.TrimSpace(req.Consumer)
	if durable == "" {
		durable = defaultDurableName(req.Subject)
	}
	if err := validateDurableName(durable); err != nil {
		return ConsumeResult{}, fmt.Errorf("%w: %v", ErrInvalidSubject, err)
	}

	sub, err := c.subscription(req.Subject, durable, family)
	if err != nil {
		return ConsumeResult{}, err
	}

	wait := c.wait
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < wait {
			wait = remaining
		}
	}

	msgs, err := sub.Fetch(batch, nats.MaxWait(wait))
	if err != nil {
		if errorsIsTimeout(err) {
			c.log.Info("event consume empty",
				"span", "events.consume",
				"subject", req.Subject,
				"delivered", 0,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return ConsumeResult{Messages: []DeliveredMessage{}}, nil
		}
		return ConsumeResult{}, fmt.Errorf("fetch: %w", err)
	}

	out := make([]DeliveredMessage, 0, len(msgs))
	for _, msg := range msgs {
		env, envErr := UnmarshalEnvelope(msg.Data)
		meta, metaErr := msg.Metadata()
		ackToken := ""
		if metaErr == nil && meta != nil {
			ackToken = encodeAckToken(meta.Stream, meta.Sequence.Stream, durable)
		} else {
			ackToken = encodeAckToken(family, 0, durable)
		}
		deliveryCount := 1
		if metaErr == nil && meta != nil {
			deliveryCount = int(meta.NumDelivered)
		}
		if envErr != nil {
			// Legacy subject-based pull still auto-acks (HTTP path uses durable Store).
			_ = msg.Ack()
			out = append(out, DeliveredMessage{
				EventID:       NewEventID(),
				Subject:       msg.Subject,
				Time:          time.Now().UTC().Truncate(time.Millisecond),
				Data:          json.RawMessage(msg.Data),
				AckToken:      ackToken,
				DeliveryCount: deliveryCount,
			})
			continue
		}
		_ = msg.Ack()
		out = append(out, DeliveredMessage{
			EventID:       env.ID,
			Subject:       env.Subject,
			Time:          env.Time,
			Source:        env.Source,
			Data:          env.Data,
			AckToken:      ackToken,
			DeliveryCount: deliveryCount,
		})
	}

	c.metrics.Consumed.Add(uint64(len(out)))
	c.log.Info("event consume",
		"span", "events.consume",
		"subject", req.Subject,
		"delivered", len(out),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return ConsumeResult{Messages: out}, nil
}

func (c *Consumer) subscription(subject, durable, family string) (*nats.Subscription, error) {
	key := family + "|" + durable + "|" + subject
	c.mu.Lock()
	defer c.mu.Unlock()
	if sub, ok := c.subs[key]; ok && sub.IsValid() {
		return sub, nil
	}
	sub, err := c.js.PullSubscribe(subject, durable,
		nats.BindStream(family),
		nats.DeliverAll(),
		nats.AckExplicit(),
	)
	if err != nil {
		return nil, fmt.Errorf("pull subscribe: %w", err)
	}
	c.subs[key] = sub
	return sub, nil
}

func defaultDurableName(subject string) string {
	var b strings.Builder
	b.WriteString("forge_pull_")
	for _, r := range subject {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}
	name := b.String()
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func validateDurableName(name string) error {
	if name == "" {
		return fmt.Errorf("consumer name is empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("consumer name too long")
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("consumer name has invalid character")
	}
	return nil
}

type ackTokenPayload struct {
	Stream   string `json:"stream"`
	Seq      uint64 `json:"seq"`
	Consumer string `json:"consumer"`
}

func encodeAckToken(stream string, seq uint64, consumer string) string {
	raw, err := json.Marshal(ackTokenPayload{Stream: stream, Seq: seq, Consumer: consumer})
	if err != nil {
		return "ack_stub"
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func errorsIsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if err == nats.ErrTimeout {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "Timeout")
}
