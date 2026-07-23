package natsx

import (
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestPlatformStreamsPresent verifies the epic-11 platform streams exist on the
// shared Compose NATS. Used by Makefile integration checks after forge-events boots.
func TestPlatformStreamsPresent(t *testing.T) {
	if os.Getenv("FORGE_EVENTS_EXPECT_STREAMS") != "1" {
		t.Skip("set FORGE_EVENTS_EXPECT_STREAMS=1 to assert platform streams")
	}
	url := os.Getenv("FORGE_NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:5002"
	}
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("NATS connect %s: %v", url, err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	families := []string{"build", "deployment", "runtime", "application", "agent"}
	names := StreamNames(families, true)
	if err := StreamsPresent(js, names); err != nil {
		t.Fatalf("platform streams: %v", err)
	}
	for _, name := range families {
		info, err := js.StreamInfo(name)
		if err != nil {
			t.Fatalf("StreamInfo(%s): %v", name, err)
		}
		want := []string{name + ".>"}
		if !subjectsCompatible(info.Config.Subjects, want) {
			t.Fatalf("stream %s subjects = %v, want %v", name, info.Config.Subjects, want)
		}
	}
	for _, family := range families {
		name := DLQStreamName(family)
		info, err := js.StreamInfo(name)
		if err != nil {
			t.Fatalf("StreamInfo(%s): %v", name, err)
		}
		want := []string{"dlq." + family + ".>"}
		if !subjectsCompatible(info.Config.Subjects, want) {
			t.Fatalf("stream %s subjects = %v, want %v", name, info.Config.Subjects, want)
		}
	}
}
