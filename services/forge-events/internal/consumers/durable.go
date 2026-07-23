package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"forge.local/services/forge-events/internal/events"

	"github.com/nats-io/nats.go"
)

// Sentinel errors for durable consumer management.
var (
	ErrInvalidConfig = errors.New("invalid consumer config")
	ErrConflict      = errors.New("consumer config conflict")
	ErrNotFound      = errors.New("consumer not found")
	ErrNotReady      = errors.New("jetstream not ready")
)

// JS is the JetStream surface used by Store (for tests).
type JS interface {
	AddConsumer(stream string, cfg *nats.ConsumerConfig, opts ...nats.JSOpt) (*nats.ConsumerInfo, error)
	ConsumerInfo(streamName, consumer string, opts ...nats.JSOpt) (*nats.ConsumerInfo, error)
	DeleteConsumer(stream, consumer string, opts ...nats.JSOpt) error
	PullSubscribe(subj, durable string, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// CreateRequest is the input for registering a durable consumer.
type CreateRequest struct {
	Name          string
	Subject       string
	AckWaitS      int
	MaxDeliveries int
}

// ConsumerInfo is the API-facing durable consumer record.
type ConsumerInfo struct {
	Name          string    `json:"name"`
	Subject       string    `json:"subject"`
	AckWaitS      int       `json:"ack_wait_s"`
	MaxDeliveries int       `json:"max_deliveries"`
	CreatedAt     time.Time `json:"created_at"`
	Stream        string    `json:"stream"`
}

// ConsumeRequest pulls a batch from a named durable consumer.
type ConsumeRequest struct {
	Consumer string
	Batch    int
}

// Metrics tracks durable consume counters.
type Metrics struct {
	Consumed atomic.Uint64
	Parked   atomic.Uint64
}

// Store creates and pull-consumes named JetStream durable consumers.
type Store struct {
	js                   JS
	families             []string
	defaultAckWaitS      int
	defaultMaxDeliveries int
	maxBatch             int
	wait                 time.Duration
	ack                  *AckManager
	log                  *slog.Logger
	metrics              *Metrics

	mu       sync.Mutex
	registry map[string]ConsumerInfo
	subs     map[string]*nats.Subscription
}

// NewStore constructs a durable consumer Store.
func NewStore(
	js JS,
	families []string,
	defaultAckWaitS, defaultMaxDeliveries, maxBatch int,
	wait time.Duration,
	ack *AckManager,
	log *slog.Logger,
	metrics *Metrics,
) *Store {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	if ack == nil {
		ack = NewAckManager(time.Duration(defaultAckWaitS)*time.Second*2, log, nil)
	}
	if defaultAckWaitS <= 0 {
		defaultAckWaitS = 30
	}
	if defaultMaxDeliveries <= 0 {
		defaultMaxDeliveries = 5
	}
	if maxBatch <= 0 {
		maxBatch = 100
	}
	if wait <= 0 {
		wait = 2 * time.Second
	}
	return &Store{
		js:                   js,
		families:             append([]string(nil), families...),
		defaultAckWaitS:      defaultAckWaitS,
		defaultMaxDeliveries: defaultMaxDeliveries,
		maxBatch:             maxBatch,
		wait:                 wait,
		ack:                  ack,
		log:                  log,
		metrics:              metrics,
		registry:             make(map[string]ConsumerInfo),
		subs:                 make(map[string]*nats.Subscription),
	}
}

// AckManager returns the shared ack token manager.
func (s *Store) AckManager() *AckManager {
	return s.ack
}

// Create registers (or idempotently returns) a JetStream durable consumer.
func (s *Store) Create(req CreateRequest) (ConsumerInfo, error) {
	name := strings.TrimSpace(req.Name)
	if err := validateConsumerName(name); err != nil {
		return ConsumerInfo{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	subject := strings.TrimSpace(req.Subject)
	family, err := events.FamilyForSubject(subject, s.families)
	if err != nil {
		return ConsumerInfo{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	ackWaitS := req.AckWaitS
	if ackWaitS <= 0 {
		ackWaitS = s.defaultAckWaitS
	}
	maxDeliveries := req.MaxDeliveries
	if maxDeliveries <= 0 {
		maxDeliveries = s.defaultMaxDeliveries
	}
	if ackWaitS < 1 {
		return ConsumerInfo{}, fmt.Errorf("%w: ack_wait_s must be >= 1", ErrInvalidConfig)
	}
	if maxDeliveries < 1 {
		return ConsumerInfo{}, fmt.Errorf("%w: max_deliveries must be >= 1", ErrInvalidConfig)
	}

	if s.js == nil {
		return ConsumerInfo{}, ErrNotReady
	}

	want := ConsumerInfo{
		Name:          name,
		Subject:       subject,
		AckWaitS:      ackWaitS,
		MaxDeliveries: maxDeliveries,
		Stream:        family,
		CreatedAt:     time.Now().UTC().Truncate(time.Millisecond),
	}

	s.mu.Lock()
	if existing, ok := s.registry[name]; ok {
		s.mu.Unlock()
		if consumerConfigEqual(existing, want) {
			return existing, nil
		}
		return ConsumerInfo{}, fmt.Errorf("%w: consumer %q exists with different config", ErrConflict, name)
	}
	s.mu.Unlock()

	if info, err := s.js.ConsumerInfo(family, name); err == nil && info != nil {
		existing := consumerInfoFromNATS(info, subject)
		if !compatibleNATSConsumer(info, subject, ackWaitS, maxDeliveries) {
			return ConsumerInfo{}, fmt.Errorf("%w: consumer %q exists with different config", ErrConflict, name)
		}
		s.mu.Lock()
		s.registry[name] = existing
		s.mu.Unlock()
		return existing, nil
	} else if err != nil && !errors.Is(err, nats.ErrConsumerNotFound) {
		// ConsumerInfo returns ErrConsumerNotFound when missing; other errors are fatal.
		if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "consumer not found") {
			return ConsumerInfo{}, fmt.Errorf("consumer info: %w", err)
		}
	}

	cfg := &nats.ConsumerConfig{
		Durable:       name,
		Name:          name,
		FilterSubject: subject,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       time.Duration(ackWaitS) * time.Second,
		MaxDeliver:    maxDeliveries,
		DeliverPolicy: nats.DeliverAllPolicy,
		ReplayPolicy:  nats.ReplayInstantPolicy,
		// Constant ack_wait delay between redeliveries (bounded by max_deliveries).
		// Progressive BackOff is avoided so AckWait round-trips cleanly for idempotent create.
	}
	if _, err := s.js.AddConsumer(family, cfg); err != nil {
		// Race: another process created it.
		if info, infoErr := s.js.ConsumerInfo(family, name); infoErr == nil && info != nil {
			if !compatibleNATSConsumer(info, subject, ackWaitS, maxDeliveries) {
				return ConsumerInfo{}, fmt.Errorf("%w: consumer %q exists with different config", ErrConflict, name)
			}
			existing := consumerInfoFromNATS(info, subject)
			s.mu.Lock()
			s.registry[name] = existing
			s.mu.Unlock()
			return existing, nil
		}
		return ConsumerInfo{}, fmt.Errorf("add consumer: %w", err)
	}

	s.mu.Lock()
	s.registry[name] = want
	s.mu.Unlock()

	s.log.Info("durable consumer created",
		"span", "events.consumer.create",
		"consumer", name,
		"subject", subject,
		"stream", family,
		"ack_wait_s", ackWaitS,
		"max_deliveries", maxDeliveries,
	)
	return want, nil
}

// Get returns a registered consumer by name.
func (s *Store) Get(name string) (ConsumerInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.registry[strings.TrimSpace(name)]
	return info, ok
}

// Consume pull-fetches up to batch messages from a named durable consumer.
// Messages are NOT auto-acked; callers must ack/nak via AckManager tokens.
func (s *Store) Consume(ctx context.Context, req ConsumeRequest) (events.ConsumeResult, error) {
	start := time.Now()
	name := strings.TrimSpace(req.Consumer)
	if name == "" {
		return events.ConsumeResult{}, fmt.Errorf("%w: consumer is required", ErrInvalidConfig)
	}
	if err := validateConsumerName(name); err != nil {
		return events.ConsumeResult{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	info, ok := s.Get(name)
	if !ok {
		// Recover from JetStream if process restarted (registry empty).
		recovered, err := s.recoverConsumer(name)
		if err != nil {
			return events.ConsumeResult{}, err
		}
		info = recovered
	}

	batch := req.Batch
	if batch <= 0 {
		batch = 1
	}
	if batch > s.maxBatch {
		batch = s.maxBatch
	}
	if s.js == nil {
		return events.ConsumeResult{}, ErrNotReady
	}

	sub, err := s.subscription(info)
	if err != nil {
		return events.ConsumeResult{}, err
	}

	wait := s.wait
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < wait {
			wait = remaining
		}
	}

	msgs, err := sub.Fetch(batch, nats.MaxWait(wait))
	if err != nil {
		if errorsIsTimeout(err) {
			s.log.Info("event consume empty",
				"span", "events.consume",
				"consumer", name,
				"subject", info.Subject,
				"delivered", 0,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return events.ConsumeResult{Messages: []events.DeliveredMessage{}}, nil
		}
		return events.ConsumeResult{}, fmt.Errorf("fetch: %w", err)
	}

	out := make([]events.DeliveredMessage, 0, len(msgs))
	for _, msg := range msgs {
		meta, metaErr := msg.Metadata()
		deliveryCount := 1
		if metaErr == nil && meta != nil {
			deliveryCount = int(meta.NumDelivered)
		}

		// Max deliveries exceeded: park (terminal) for DLQ handling in 11.04.
		if deliveryCount > info.MaxDeliveries {
			s.park(msg, name, "", deliveryCount)
			continue
		}

		env, envErr := events.UnmarshalEnvelope(msg.Data)
		eventID := ""
		subject := msg.Subject
		var data json.RawMessage
		var src string
		var ts time.Time
		if envErr != nil {
			eventID = events.NewEventID()
			ts = time.Now().UTC().Truncate(time.Millisecond)
			data = json.RawMessage(msg.Data)
		} else {
			eventID = env.ID
			subject = env.Subject
			ts = env.Time
			src = env.Source
			data = env.Data
		}

		if deliveryCount >= info.MaxDeliveries {
			// Final delivery attempt — still expose to consumer; if they nak,
			// JetStream will not redeliver (parked). Log terminal state.
			s.log.Info("event final delivery",
				"span", "events.redeliver",
				"consumer", name,
				"event_id", eventID,
				"delivery_count", deliveryCount,
				"max_deliveries", info.MaxDeliveries,
			)
		}

		token := s.ack.Register(msg, name, eventID, deliveryCount)
		out = append(out, events.DeliveredMessage{
			EventID:       eventID,
			Subject:       subject,
			Time:          ts,
			Source:        src,
			Data:          data,
			AckToken:      token,
			DeliveryCount: deliveryCount,
		})
		if deliveryCount > 1 {
			s.ack.metrics.Redelivered.Add(1)
			s.log.Info("event redelivered",
				"span", "events.redeliver",
				"consumer", name,
				"event_id", eventID,
				"delivery_count", deliveryCount,
			)
		}
	}

	s.metrics.Consumed.Add(uint64(len(out)))
	s.log.Info("event consume",
		"span", "events.consume",
		"consumer", name,
		"subject", info.Subject,
		"delivered", len(out),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return events.ConsumeResult{Messages: out}, nil
}

func (s *Store) park(msg *nats.Msg, consumer, eventID string, deliveryCount int) {
	if eventID == "" {
		if env, err := events.UnmarshalEnvelope(msg.Data); err == nil {
			eventID = env.ID
		}
	}
	_ = msg.Term()
	s.metrics.Parked.Add(1)
	s.log.Warn("event parked (max deliveries exceeded)",
		"span", "events.park",
		"consumer", consumer,
		"event_id", eventID,
		"delivery_count", deliveryCount,
	)
}

func (s *Store) recoverConsumer(name string) (ConsumerInfo, error) {
	if s.js == nil {
		return ConsumerInfo{}, ErrNotReady
	}
	for _, family := range s.families {
		info, err := s.js.ConsumerInfo(family, name)
		if err != nil {
			continue
		}
		subject := info.Config.FilterSubject
		if subject == "" {
			return ConsumerInfo{}, fmt.Errorf("%w: consumer %q has no filter subject", ErrInvalidConfig, name)
		}
		rec := consumerInfoFromNATS(info, subject)
		s.mu.Lock()
		s.registry[name] = rec
		s.mu.Unlock()
		return rec, nil
	}
	return ConsumerInfo{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

func (s *Store) subscription(info ConsumerInfo) (*nats.Subscription, error) {
	key := info.Stream + "|" + info.Name
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub, ok := s.subs[key]; ok && sub.IsValid() {
		return sub, nil
	}
	sub, err := s.js.PullSubscribe(info.Subject, info.Name,
		nats.Bind(info.Stream, info.Name),
	)
	if err != nil {
		// Fall back to bind-stream create if bind fails on fresh consumer.
		sub, err = s.js.PullSubscribe(info.Subject, info.Name,
			nats.BindStream(info.Stream),
			nats.AckExplicit(),
		)
		if err != nil {
			return nil, fmt.Errorf("pull subscribe: %w", err)
		}
	}
	s.subs[key] = sub
	return sub, nil
}

func consumerConfigEqual(a, b ConsumerInfo) bool {
	return a.Name == b.Name &&
		a.Subject == b.Subject &&
		a.AckWaitS == b.AckWaitS &&
		a.MaxDeliveries == b.MaxDeliveries &&
		a.Stream == b.Stream
}

func compatibleNATSConsumer(info *nats.ConsumerInfo, subject string, ackWaitS, maxDeliveries int) bool {
	if info == nil {
		return false
	}
	cfg := info.Config
	if cfg.FilterSubject != "" && cfg.FilterSubject != subject {
		return false
	}
	if int(cfg.AckWait.Seconds()) != ackWaitS {
		return false
	}
	if cfg.MaxDeliver != maxDeliveries {
		return false
	}
	return true
}

func consumerInfoFromNATS(info *nats.ConsumerInfo, subject string) ConsumerInfo {
	ackWaitS := int(info.Config.AckWait.Seconds())
	if ackWaitS <= 0 {
		ackWaitS = 30
	}
	maxDeliveries := info.Config.MaxDeliver
	if maxDeliveries <= 0 {
		maxDeliveries = 5
	}
	if subject == "" {
		subject = info.Config.FilterSubject
	}
	created := info.Created
	if created.IsZero() {
		created = time.Now().UTC()
	}
	return ConsumerInfo{
		Name:          info.Name,
		Subject:       subject,
		AckWaitS:      ackWaitS,
		MaxDeliveries: maxDeliveries,
		Stream:        info.Stream,
		CreatedAt:     created.UTC().Truncate(time.Millisecond),
	}
}

func validateConsumerName(name string) error {
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
