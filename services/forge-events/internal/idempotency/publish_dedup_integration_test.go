package idempotency_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-events/internal/events"
	natsx "forge.local/services/forge-events/internal/nats"

	"github.com/nats-io/nats.go"
)

func TestPublishSameIdempotencyKeySingleMessage(t *testing.T) {
	url := os.Getenv("FORGE_NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:5002"
	}
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Skipf("NATS not available: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	stream := fmt.Sprintf("idedup_%d", time.Now().UnixNano())
	subject := stream + ".evt"
	t.Cleanup(func() { _ = js.DeleteStream(stream) })
	if err := natsx.BootstrapStreams(js, []natsx.StreamSpec{{
		Name:       stream,
		Subjects:   []string{stream + ".>"},
		Duplicates: 120 * time.Second,
	}}, nil); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	pub := events.NewPublisher(js, []string{stream}, 256*1024, nil, nil)
	key := fmt.Sprintf("k-%d", time.Now().UnixNano())
	payload := json.RawMessage(`{"service":"demo","reason":"oom","occurred_at":"2026-07-22T14:00:00Z"}`)
	// Subject must match family — FamilyForSubject splits on first "."
	// so use stream as family prefix via a custom publisher family list.
	r1, err := pub.Publish(context.Background(), events.PublishRequest{
		Subject:        subject,
		Data:           payload,
		Source:         "t",
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	r2, err := pub.Publish(context.Background(), events.PublishRequest{
		Subject:        subject,
		Data:           payload,
		Source:         "t",
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if r1.EventID != r2.EventID || r1.Seq != r2.Seq {
		t.Fatalf("results differ: %#v vs %#v", r1, r2)
	}
	if !r2.Duplicate {
		t.Fatal("expected Duplicate=true on second publish")
	}
	info, err := js.StreamInfo(stream)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("msgs = %d, want 1", info.State.Msgs)
	}
}
