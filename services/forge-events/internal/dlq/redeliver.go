package dlq

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"forge.local/services/forge-events/internal/events"

	"github.com/nats-io/nats.go"
)

// RedeliverResult is returned after a successful DLQ replay.
type RedeliverResult struct {
	RepublishedTo string `json:"republished_to"`
	EventID       string `json:"event_id"`
}

// Redeliverer republishes a DLQ message to its original subject.
type Redeliverer struct {
	js      JS
	store   *Store
	log     *slog.Logger
	metrics *Metrics
}

// NewRedeliverer constructs a Redeliverer.
func NewRedeliverer(js JS, store *Store, log *slog.Logger, metrics *Metrics) *Redeliverer {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &Redeliverer{js: js, store: store, log: log, metrics: metrics}
}

// Redeliver loads a DLQ entry and publishes its envelope to the original subject.
// The envelope event id is preserved; NATS Msg-Id uses a unique redeliver id so
// JetStream dedup does not reject the replay.
func (r *Redeliverer) Redeliver(ctx context.Context, dlqID string) (RedeliverResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return RedeliverResult{}, ErrNotFound
	}
	if r.js == nil {
		return RedeliverResult{}, ErrNotReady
	}
	dlqID = strings.TrimSpace(dlqID)
	entry, err := r.store.Get(dlqID)
	if err != nil {
		return RedeliverResult{}, err
	}

	payload := append([]byte(nil), entry.Payload...)
	env, envErr := events.UnmarshalEnvelope(payload)
	eventID := entry.EventID
	if envErr == nil {
		// Preserve original event id on the envelope for consumer idempotency.
		if entry.EventID != "" {
			env.ID = entry.EventID
		}
		eventID = env.ID
		var marshalErr error
		payload, marshalErr = env.Marshal()
		if marshalErr != nil {
			return RedeliverResult{}, fmt.Errorf("marshal envelope: %w", marshalErr)
		}
	}

	msg := &nats.Msg{
		Subject: entry.OriginalSubject,
		Data:    payload,
		Header:  nats.Header{},
	}
	// Unique Msg-Id avoids JetStream duplicate-window rejection of the original id.
	msg.Header.Set(nats.MsgIdHdr, "redeliver_"+dlqID+"_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	msg.Header.Set(HeaderEventID, eventID)
	msg.Header.Set(HeaderDLQID, dlqID)

	if _, err := r.js.PublishMsg(msg); err != nil {
		return RedeliverResult{}, fmt.Errorf("%w: %v", ErrNotReady, err)
	}

	r.metrics.Redelivered.Add(1)
	r.log.Info("dlq message redelivered",
		"span", "events.dlq.redeliver",
		"dlq_id", dlqID,
		"event_id", eventID,
		"original_subject", entry.OriginalSubject,
		"consumer", entry.Consumer,
	)
	return RedeliverResult{
		RepublishedTo: entry.OriginalSubject,
		EventID:       eventID,
	}, nil
}
