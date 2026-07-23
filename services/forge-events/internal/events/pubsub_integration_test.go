package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestPublishConsumeRoundTrip(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()

	families := []string{"build", "deployment", "runtime", "application", "agent"}
	if err := ensurePlatformStreams(js, families); err != nil {
		t.Fatalf("streams: %v", err)
	}

	metrics := &Metrics{}
	pub := NewPublisher(js, families, 256*1024, nil, metrics)
	cons := NewConsumer(js, families, 100, 500*time.Millisecond, nil, metrics)

	subject := fmt.Sprintf("application.crashed.test_%d", time.Now().UnixNano())
	consumer := fmt.Sprintf("test_cons_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = js.DeleteConsumer("application", consumer)
	})

	payload := json.RawMessage(`{"service":"demo","reason":"oom"}`)
	res, err := pub.Publish(context.Background(), PublishRequest{
		Subject: subject,
		Data:    payload,
		Source:  "runtime",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Stream != "application" || res.Seq == 0 {
		t.Fatalf("publish result = %#v", res)
	}

	batch, err := cons.Consume(context.Background(), ConsumeRequest{
		Subject:  subject,
		Batch:    10,
		Consumer: consumer,
	})
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(batch.Messages) != 1 {
		t.Fatalf("messages = %d, want 1: %#v", len(batch.Messages), batch.Messages)
	}
	msg := batch.Messages[0]
	if msg.EventID != res.EventID {
		t.Fatalf("event_id = %q, want %q", msg.EventID, res.EventID)
	}
	if msg.Subject != subject {
		t.Fatalf("subject = %q, want %q", msg.Subject, subject)
	}
	if string(msg.Data) != string(payload) {
		t.Fatalf("data = %s, want %s", msg.Data, payload)
	}
	if msg.AckToken == "" {
		t.Fatal("expected ack_token")
	}
}

func TestPublishUnknownSubject(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()
	pub := NewPublisher(js, []string{"application"}, 1024, nil, nil)
	_, err := pub.Publish(context.Background(), PublishRequest{
		Subject: "nope.bad",
		Data:    json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConsumePreservesStreamOrder(t *testing.T) {
	js, cleanup := testJS(t)
	defer cleanup()
	families := []string{"application"}
	if err := ensurePlatformStreams(js, families); err != nil {
		t.Fatalf("streams: %v", err)
	}
	pub := NewPublisher(js, families, 1024, nil, nil)
	cons := NewConsumer(js, families, 100, 500*time.Millisecond, nil, nil)

	subject := fmt.Sprintf("application.ordered_%d", time.Now().UnixNano())
	consumer := fmt.Sprintf("test_order_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = js.DeleteConsumer("application", consumer)
	})

	var ids []string
	for i := 0; i < 3; i++ {
		res, err := pub.Publish(context.Background(), PublishRequest{
			Subject: subject,
			Data:    json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
			Source:  "test",
		})
		if err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
		ids = append(ids, res.EventID)
	}

	batch, err := cons.Consume(context.Background(), ConsumeRequest{
		Subject:  subject,
		Batch:    10,
		Consumer: consumer,
	})
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(batch.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(batch.Messages))
	}
	for i, msg := range batch.Messages {
		if msg.EventID != ids[i] {
			t.Fatalf("order[%d] = %q, want %q", i, msg.EventID, ids[i])
		}
		var body struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(msg.Data, &body); err != nil {
			t.Fatalf("data json: %v", err)
		}
		if body.N != i {
			t.Fatalf("data.n = %d, want %d", body.N, i)
		}
	}
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

func ensurePlatformStreams(js nats.JetStreamContext, families []string) error {
	for _, name := range families {
		info, err := js.StreamInfo(name)
		if err == nil {
			_ = info
			continue
		}
		if err != nats.ErrStreamNotFound {
			return err
		}
		_, err = js.AddStream(&nats.StreamConfig{
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
