package dlq_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-events/internal/consumers"
	"forge.local/services/forge-events/internal/dlq"
	"forge.local/services/forge-events/internal/events"
	natsx "forge.local/services/forge-events/internal/nats"

	"github.com/nats-io/nats.go"
)

func TestMaxDeliveriesRoutesToDLQInspectRedeliverDelete(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()

	family := "application"
	if err := ensureFamilyAndDLQ(js, family); err != nil {
		t.Fatalf("streams: %v", err)
	}

	dlqStore := dlq.NewStore(js)
	dlqMetrics := &dlq.Metrics{}
	router := dlq.NewRouter(js, dlqStore, true, nil, dlqMetrics)
	redeliverer := dlq.NewRedeliverer(js, dlqStore, nil, dlqMetrics)

	ack := consumers.NewAckManager(60*time.Second, nil, nil)
	store := consumers.NewStore(js, []string{family}, 30, 5, 100, 300*time.Millisecond, ack, nil, nil)
	store.SetDLQRouter(router)

	pub := events.NewPublisher(js, []string{family}, 1024, nil, nil)
	subject := fmt.Sprintf("application.dlq_%d", time.Now().UnixNano())
	name := fmt.Sprintf("dlq_worker_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = js.DeleteConsumer(family, name) })

	const maxDeliveries = 3
	if _, err := store.Create(consumers.CreateRequest{
		Name: name, Subject: subject, AckWaitS: 30, MaxDeliveries: maxDeliveries,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pubRes, err := pub.Publish(context.Background(), events.PublishRequest{
		Subject: subject, Data: json.RawMessage(`{"service":"poison"}`), Source: "test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i := 1; i <= maxDeliveries; i++ {
		msg := waitOne(t, store, name, 5*time.Second)
		if msg.DeliveryCount != i {
			t.Fatalf("delivery %d: count=%d", i, msg.DeliveryCount)
		}
		if err := store.AckManager().Nak(msg.AckToken, 0); err != nil {
			t.Fatalf("Nak %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	var entries []dlq.Entry
	for time.Now().Before(deadline) {
		entries = dlqStore.List(dlq.ListFilter{Subject: subject, Consumer: name})
		if len(entries) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(entries) < 1 {
		t.Fatal("expected DLQ entry after max deliveries")
	}
	entry := entries[0]
	if entry.EventID != pubRes.EventID {
		t.Fatalf("dlq event_id = %q, want %q", entry.EventID, pubRes.EventID)
	}
	if entry.OriginalSubject != subject {
		t.Fatalf("original_subject = %q", entry.OriginalSubject)
	}
	if entry.Consumer != name {
		t.Fatalf("consumer = %q", entry.Consumer)
	}
	if entry.DeliveryCount < maxDeliveries {
		t.Fatalf("delivery_count = %d, want >= %d", entry.DeliveryCount, maxDeliveries)
	}
	if entry.LastError == "" {
		t.Fatal("expected last_error")
	}

	detail, err := dlqStore.Detail(entry.DLQID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(detail.Envelope) == 0 {
		t.Fatal("detail missing envelope")
	}

	// Healthy consumer on a fresh durable should receive the redelivered event.
	healthy := fmt.Sprintf("healthy_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = js.DeleteConsumer(family, healthy) })
	if _, err := store.Create(consumers.CreateRequest{
		Name: healthy, Subject: subject, AckWaitS: 30, MaxDeliveries: 5,
	}); err != nil {
		t.Fatalf("Create healthy: %v", err)
	}

	res, err := redeliverer.Redeliver(context.Background(), entry.DLQID)
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	if res.EventID != pubRes.EventID {
		t.Fatalf("redeliver event_id = %q, want %q", res.EventID, pubRes.EventID)
	}

	replayed := waitOne(t, store, healthy, 5*time.Second)
	if replayed.EventID != pubRes.EventID {
		t.Fatalf("replayed event_id = %q, want %q", replayed.EventID, pubRes.EventID)
	}
	if err := store.AckManager().Ack(replayed.AckToken); err != nil {
		t.Fatalf("Ack replayed: %v", err)
	}

	if err := dlqStore.Delete(entry.DLQID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := dlqStore.Get(entry.DLQID); err == nil {
		t.Fatal("expected not found after delete")
	}
}

func waitOne(t *testing.T, store *consumers.Store, consumer string, timeout time.Duration) events.DeliveredMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		batch, err := store.Consume(context.Background(), consumers.ConsumeRequest{Consumer: consumer, Batch: 1})
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
		if len(batch.Messages) == 1 {
			return batch.Messages[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no message for consumer %s", consumer)
	return events.DeliveredMessage{}
}

func testJS(t *testing.T) (nats.JetStreamContext, func()) {
	t.Helper()
	url := os.Getenv("FORGE_NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:5002"
	}
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Skipf("NATS not available at %s: %v", url, err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		t.Fatalf("JetStream: %v", err)
	}
	return js, func() { nc.Close() }
}

func ensureFamilyAndDLQ(js nats.JetStreamContext, family string) error {
	if err := natsx.BootstrapStreams(js, natsx.BootstrapSpecs([]string{family}, true), nil); err != nil {
		return err
	}
	return nil
}
