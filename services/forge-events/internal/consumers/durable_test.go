package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-events/internal/events"

	"github.com/nats-io/nats.go"
)

func TestCreateDurableIdempotentAndConflict(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()
	if err := ensureStreams(js, []string{"application"}); err != nil {
		t.Fatalf("streams: %v", err)
	}

	store := newTestStore(js)
	name := fmt.Sprintf("idem_%d", time.Now().UnixNano())
	subject := fmt.Sprintf("application.crash_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = js.DeleteConsumer("application", name) })

	first, err := store.Create(CreateRequest{
		Name: name, Subject: subject, AckWaitS: 5, MaxDeliveries: 3,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := store.Create(CreateRequest{
		Name: name, Subject: subject, AckWaitS: 5, MaxDeliveries: 3,
	})
	if err != nil {
		t.Fatalf("Create idempotent: %v", err)
	}
	if first.Name != second.Name || first.Subject != second.Subject {
		t.Fatalf("idempotent mismatch: %#v vs %#v", first, second)
	}

	_, err = store.Create(CreateRequest{
		Name: name, Subject: subject, AckWaitS: 10, MaxDeliveries: 3,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict err = %v, want ErrConflict", err)
	}
}

func TestAckAdvancesNakRedelivers(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()
	if err := ensureStreams(js, []string{"application"}); err != nil {
		t.Fatalf("streams: %v", err)
	}
	store := newTestStore(js)
	pub := events.NewPublisher(js, []string{"application"}, 1024, nil, nil)

	subject := fmt.Sprintf("application.ack_%d", time.Now().UnixNano())
	name := fmt.Sprintf("ack_worker_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = js.DeleteConsumer("application", name) })

	if _, err := store.Create(CreateRequest{
		Name: name, Subject: subject, AckWaitS: 30, MaxDeliveries: 5,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := pub.Publish(context.Background(), events.PublishRequest{
		Subject: subject, Data: json.RawMessage(`{"n":1}`), Source: "test",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	batch, err := store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(batch.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(batch.Messages))
	}
	msg := batch.Messages[0]
	if msg.DeliveryCount != 1 {
		t.Fatalf("delivery_count = %d, want 1", msg.DeliveryCount)
	}
	if msg.EventID != res.EventID {
		t.Fatalf("event_id = %q, want %q", msg.EventID, res.EventID)
	}
	if err := store.AckManager().Nak(msg.AckToken, 0); err != nil {
		t.Fatalf("Nak: %v", err)
	}

	// Immediate nak → redelivery with incremented count.
	deadline := time.Now().Add(5 * time.Second)
	var redelivered events.DeliveredMessage
	for time.Now().Before(deadline) {
		batch, err = store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
		if err != nil {
			t.Fatalf("Consume redeliver: %v", err)
		}
		if len(batch.Messages) == 1 {
			redelivered = batch.Messages[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if redelivered.AckToken == "" {
		t.Fatal("expected redelivered message")
	}
	if redelivered.DeliveryCount != 2 {
		t.Fatalf("delivery_count = %d, want 2", redelivered.DeliveryCount)
	}
	if err := store.AckManager().Ack(redelivered.AckToken); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// After ack, no further delivery.
	batch, err = store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
	if err != nil {
		t.Fatalf("Consume after ack: %v", err)
	}
	if len(batch.Messages) != 0 {
		t.Fatalf("after ack got %d messages, want 0", len(batch.Messages))
	}
}

func TestAckWaitRedeliveryAndRestart(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()
	if err := ensureStreams(js, []string{"application"}); err != nil {
		t.Fatalf("streams: %v", err)
	}
	store := newTestStore(js)
	pub := events.NewPublisher(js, []string{"application"}, 1024, nil, nil)

	subject := fmt.Sprintf("application.wait_%d", time.Now().UnixNano())
	name := fmt.Sprintf("wait_worker_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = js.DeleteConsumer("application", name) })

	if _, err := store.Create(CreateRequest{
		Name: name, Subject: subject, AckWaitS: 2, MaxDeliveries: 5,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ackedID := ""
	{
		res, err := pub.Publish(context.Background(), events.PublishRequest{
			Subject: subject, Data: json.RawMessage(`{"which":"ack"}`), Source: "test",
		})
		if err != nil {
			t.Fatalf("Publish ack msg: %v", err)
		}
		ackedID = res.EventID
		batch, err := store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
		if err != nil || len(batch.Messages) != 1 {
			t.Fatalf("Consume ack msg: err=%v n=%d", err, len(batch.Messages))
		}
		if err := store.AckManager().Ack(batch.Messages[0].AckToken); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}

	unackedID := ""
	{
		res, err := pub.Publish(context.Background(), events.PublishRequest{
			Subject: subject, Data: json.RawMessage(`{"which":"unacked"}`), Source: "test",
		})
		if err != nil {
			t.Fatalf("Publish unacked: %v", err)
		}
		unackedID = res.EventID
		batch, err := store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
		if err != nil || len(batch.Messages) != 1 {
			t.Fatalf("Consume unacked: err=%v n=%d", err, len(batch.Messages))
		}
		if batch.Messages[0].EventID != unackedID {
			t.Fatalf("got %q, want unacked %q", batch.Messages[0].EventID, unackedID)
		}
		// Do not ack — simulate crash by dropping in-memory store.
	}

	// New store (consumer restart): registry empty, recovers from JetStream.
	store2 := newTestStore(js)
	deadline := time.Now().Add(6 * time.Second)
	var got events.DeliveredMessage
	for time.Now().Before(deadline) {
		batch, err := store2.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 10})
		if err != nil {
			t.Fatalf("Consume after restart: %v", err)
		}
		for _, m := range batch.Messages {
			if m.EventID == ackedID {
				t.Fatalf("acked message reprocessed after restart")
			}
			if m.EventID == unackedID {
				got = m
			}
		}
		if got.AckToken != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got.AckToken == "" {
		t.Fatal("unacked message not redelivered after restart")
	}
	if got.DeliveryCount < 2 {
		t.Fatalf("delivery_count = %d, want >= 2", got.DeliveryCount)
	}
	if err := store2.AckManager().Ack(got.AckToken); err != nil {
		t.Fatalf("Ack after restart: %v", err)
	}
}

func TestMaxDeliveriesParks(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()
	if err := ensureStreams(js, []string{"application"}); err != nil {
		t.Fatalf("streams: %v", err)
	}
	store := newTestStore(js)
	pub := events.NewPublisher(js, []string{"application"}, 1024, nil, nil)

	subject := fmt.Sprintf("application.max_%d", time.Now().UnixNano())
	name := fmt.Sprintf("max_worker_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = js.DeleteConsumer("application", name) })

	const maxDeliveries = 3
	if _, err := store.Create(CreateRequest{
		Name: name, Subject: subject, AckWaitS: 30, MaxDeliveries: maxDeliveries,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := pub.Publish(context.Background(), events.PublishRequest{
		Subject: subject, Data: json.RawMessage(`{"fail":true}`), Source: "test",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for i := 1; i <= maxDeliveries; i++ {
		var msg events.DeliveredMessage
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			batch, err := store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
			if err != nil {
				t.Fatalf("Consume %d: %v", i, err)
			}
			if len(batch.Messages) == 1 {
				msg = batch.Messages[0]
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if msg.AckToken == "" {
			t.Fatalf("delivery %d: no message", i)
		}
		if msg.DeliveryCount != i {
			t.Fatalf("delivery %d: delivery_count = %d", i, msg.DeliveryCount)
		}
		if err := store.AckManager().Nak(msg.AckToken, 0); err != nil {
			t.Fatalf("Nak %d: %v", i, err)
		}
	}

	// After max deliveries, message must not be redelivered infinitely.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		batch, err := store.Consume(context.Background(), ConsumeRequest{Consumer: name, Batch: 1})
		if err != nil {
			t.Fatalf("Consume after max: %v", err)
		}
		if len(batch.Messages) != 0 {
			t.Fatalf("message still delivered after max_deliveries: %#v", batch.Messages[0])
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func newTestStore(js nats.JetStreamContext) *Store {
	ack := NewAckManager(60*time.Second, nil, nil)
	return NewStore(js, []string{"application"}, 30, 5, 100, 300*time.Millisecond, ack, nil, nil)
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

func ensureStreams(js nats.JetStreamContext, families []string) error {
	for _, name := range families {
		if _, err := js.StreamInfo(name); err == nil {
			continue
		} else if err != nats.ErrStreamNotFound {
			return err
		}
		_, err := js.AddStream(&nats.StreamConfig{
			Name:     name,
			Subjects: []string{name + ".>"},
			Storage:  nats.MemoryStorage,
		})
		if err != nil && err != nats.ErrStreamNameAlreadyInUse {
			return err
		}
	}
	return nil
}
