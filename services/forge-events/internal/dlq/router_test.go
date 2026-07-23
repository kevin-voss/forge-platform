package dlq

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"forge.local/services/forge-events/internal/events"

	"github.com/nats-io/nats.go"
)

type mockJS struct {
	mu   sync.Mutex
	msgs []*nats.Msg
	err  error
	seq  uint64
}

func (m *mockJS) PublishMsg(msg *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	hdr := nats.Header{}
	for k, vals := range msg.Header {
		hdr[k] = append([]string(nil), vals...)
	}
	cp := &nats.Msg{
		Subject: msg.Subject,
		Data:    append([]byte(nil), msg.Data...),
		Header:  hdr,
	}
	m.msgs = append(m.msgs, cp)
	m.seq++
	return &nats.PubAck{Stream: "dlq_application", Sequence: m.seq}, nil
}

func TestRouterWritesFailureMetadata(t *testing.T) {
	js := &mockJS{}
	store := NewStore(nil)
	router := NewRouter(js, store, true, nil, nil)

	env := events.NewEnvelope("application.crashed", "test", json.RawMessage(`{"service":"poison"}`))
	payload, err := env.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	firstFailed := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)

	if err := router.Route(context.Background(), TerminalFailure{
		Payload:         payload,
		OriginalSubject: "application.crashed",
		Consumer:        "crash-worker",
		EventID:         env.ID,
		DeliveryCount:   3,
		LastError:       "handler panic",
		FirstFailedAt:   firstFailed,
		Family:          "application",
	}); err != nil {
		t.Fatalf("Route: %v", err)
	}

	js.mu.Lock()
	defer js.mu.Unlock()
	if len(js.msgs) != 1 {
		t.Fatalf("published = %d, want 1", len(js.msgs))
	}
	msg := js.msgs[0]
	if msg.Subject != "dlq.application.entry" {
		t.Fatalf("subject = %q", msg.Subject)
	}
	if got := msg.Header.Get(HeaderOriginalSubject); got != "application.crashed" {
		t.Fatalf("original_subject = %q", got)
	}
	if got := msg.Header.Get(HeaderConsumer); got != "crash-worker" {
		t.Fatalf("consumer = %q", got)
	}
	if got := msg.Header.Get(HeaderDeliveryCount); got != "3" {
		t.Fatalf("delivery_count = %q", got)
	}
	if got := msg.Header.Get(HeaderLastError); got != "handler panic" {
		t.Fatalf("last_error = %q", got)
	}
	if got := msg.Header.Get(HeaderEventID); got != env.ID {
		t.Fatalf("event_id header = %q, want %q", got, env.ID)
	}

	entries := store.List(ListFilter{})
	if len(entries) != 1 {
		t.Fatalf("index size = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.OriginalSubject != "application.crashed" || e.Consumer != "crash-worker" || e.DeliveryCount != 3 {
		t.Fatalf("entry = %#v", e)
	}
	if e.LastError != "handler panic" {
		t.Fatalf("last_error = %q", e.LastError)
	}
}

func TestRedeliverPreservesEventID(t *testing.T) {
	js := &mockJS{}
	store := NewStore(nil)
	eventID := "evt_original_123"
	env := events.Envelope{
		ID:      eventID,
		Subject: "application.crashed",
		Time:    time.Now().UTC().Truncate(time.Millisecond),
		Source:  "test",
		Data:    json.RawMessage(`{"service":"poison"}`),
	}
	payload, _ := env.Marshal()
	store.Put(Entry{
		DLQID:           "dlq_test1",
		EventID:         eventID,
		OriginalSubject: "application.crashed",
		Consumer:        "crash-worker",
		DeliveryCount:   3,
		LastError:       "fail",
		FirstFailedAt:   time.Now().UTC(),
		CreatedAt:       time.Now().UTC(),
		Family:          "application",
		Payload:         payload,
	})

	red := NewRedeliverer(js, store, nil, nil)
	res, err := red.Redeliver(context.Background(), "dlq_test1")
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if res.EventID != eventID {
		t.Fatalf("result event_id = %q, want %q", res.EventID, eventID)
	}
	if res.RepublishedTo != "application.crashed" {
		t.Fatalf("republished_to = %q", res.RepublishedTo)
	}

	js.mu.Lock()
	defer js.mu.Unlock()
	if len(js.msgs) != 1 {
		t.Fatalf("published = %d, want 1", len(js.msgs))
	}
	got, err := events.UnmarshalEnvelope(js.msgs[0].Data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != eventID {
		t.Fatalf("envelope id = %q, want %q", got.ID, eventID)
	}
	if js.msgs[0].Subject != "application.crashed" {
		t.Fatalf("subject = %q", js.msgs[0].Subject)
	}
}

func TestListFiltersBySubjectAndConsumer(t *testing.T) {
	store := NewStore(nil)
	now := time.Now().UTC()
	store.Put(Entry{DLQID: "a", EventID: "1", OriginalSubject: "application.crashed", Consumer: "crash-worker", CreatedAt: now, DeliveryCount: 3})
	store.Put(Entry{DLQID: "b", EventID: "2", OriginalSubject: "application.crashed", Consumer: "other", CreatedAt: now.Add(time.Second), DeliveryCount: 3})
	store.Put(Entry{DLQID: "c", EventID: "3", OriginalSubject: "deployment.failed", Consumer: "crash-worker", CreatedAt: now.Add(2 * time.Second), DeliveryCount: 2})

	got := store.List(ListFilter{Subject: "application.crashed", Consumer: "crash-worker"})
	if len(got) != 1 || got[0].DLQID != "a" {
		t.Fatalf("filtered = %#v, want [a]", got)
	}
	got = store.List(ListFilter{Subject: "application.crashed"})
	if len(got) != 2 {
		t.Fatalf("subject filter len = %d, want 2", len(got))
	}
	got = store.List(ListFilter{Consumer: "crash-worker"})
	if len(got) != 2 {
		t.Fatalf("consumer filter len = %d, want 2", len(got))
	}
}

func TestRouterQueuesOnPublishFailure(t *testing.T) {
	js := &mockJS{err: context.DeadlineExceeded}
	store := NewStore(nil)
	router := NewRouter(js, store, true, nil, nil)
	env := events.NewEnvelope("application.crashed", "test", json.RawMessage(`{}`))
	payload, _ := env.Marshal()

	err := router.Route(context.Background(), TerminalFailure{
		Payload:         payload,
		OriginalSubject: "application.crashed",
		Consumer:        "w",
		EventID:         env.ID,
		DeliveryCount:   3,
		Family:          "application",
	})
	if err == nil {
		t.Fatal("expected route error")
	}
	if router.PendingRetries() != 1 {
		t.Fatalf("pending retries = %d, want 1", router.PendingRetries())
	}
	if store.Size() != 0 {
		t.Fatal("store should be empty until publish succeeds")
	}

	js.mu.Lock()
	js.err = nil
	js.mu.Unlock()
	router.FlushRetries(context.Background())
	if router.PendingRetries() != 0 {
		t.Fatalf("pending after flush = %d", router.PendingRetries())
	}
	if store.Size() != 1 {
		t.Fatalf("store size = %d, want 1", store.Size())
	}
}
