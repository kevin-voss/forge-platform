package natsx

import (
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestBootstrapStreamsIdempotent(t *testing.T) {
	url := os.Getenv("FORGE_NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:5002"
	}

	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Skipf("NATS not available at %s: %v", url, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	// Use uniquely named streams so unit tests don't race compose bootstrap.
	prefix := "testbootstrap"
	specs := []StreamSpec{
		{Name: prefix + "a", Subjects: []string{prefix + "a.>"}},
		{Name: prefix + "b", Subjects: []string{prefix + "b.>"}},
	}
	t.Cleanup(func() {
		for _, s := range specs {
			_ = js.DeleteStream(s.Name)
		}
	})
	for _, s := range specs {
		_ = js.DeleteStream(s.Name)
	}

	if err := BootstrapStreams(js, specs, nil); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := BootstrapStreams(js, specs, nil); err != nil {
		t.Fatalf("second bootstrap (idempotent): %v", err)
	}
	if err := StreamsPresent(js, []string{specs[0].Name, specs[1].Name}); err != nil {
		t.Fatalf("StreamsPresent: %v", err)
	}
}

func TestSubjectsCompatible(t *testing.T) {
	if !subjectsCompatible([]string{"a.>", "b.>"}, []string{"b.>", "a.>"}) {
		t.Fatal("expected order-insensitive match")
	}
	if subjectsCompatible([]string{"a.>"}, []string{"a.>", "b.>"}) {
		t.Fatal("expected length mismatch to fail")
	}
}
