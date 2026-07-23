package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("FORGE_SERVICE_NAME", "")
	t.Setenv("FORGE_SERVICE_VERSION", "")
	t.Setenv("FORGE_LOG_LEVEL", "")
	t.Setenv("FORGE_ENV", "")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "")
	t.Setenv("FORGE_NATS_URL", "")
	t.Setenv("FORGE_EVENTS_STREAMS", "")
	t.Setenv("FORGE_EVENT_MAX_BYTES", "")
	t.Setenv("FORGE_CONSUME_MAX_BATCH", "")
	t.Setenv("FORGE_CONSUME_WAIT_MS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 4105 {
		t.Fatalf("Port = %d, want 4105", cfg.Port)
	}
	if cfg.EventMaxBytes != 256*1024 {
		t.Fatalf("EventMaxBytes = %d, want %d", cfg.EventMaxBytes, 256*1024)
	}
	if cfg.ConsumeMaxBatch != 100 {
		t.Fatalf("ConsumeMaxBatch = %d, want 100", cfg.ConsumeMaxBatch)
	}
	if cfg.ConsumeWait != 2*time.Second {
		t.Fatalf("ConsumeWait = %v, want 2s", cfg.ConsumeWait)
	}
	if cfg.ServiceName != "forge-events" {
		t.Fatalf("ServiceName = %q, want forge-events", cfg.ServiceName)
	}
	if cfg.ServiceVersion != "0.1.0" {
		t.Fatalf("ServiceVersion = %q, want 0.1.0", cfg.ServiceVersion)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.Env != "development" {
		t.Fatalf("Env = %q, want development", cfg.Env)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 10s", cfg.ShutdownGrace)
	}
	if cfg.NATSURL != "nats://nats:4222" {
		t.Fatalf("NATSURL = %q, want default nats://nats:4222", cfg.NATSURL)
	}
	if len(cfg.Streams) != len(DefaultStreams) {
		t.Fatalf("Streams len = %d, want %d", len(cfg.Streams), len(DefaultStreams))
	}
	for i, name := range DefaultStreams {
		if cfg.Streams[i] != name {
			t.Fatalf("Streams[%d] = %q, want %q", i, cfg.Streams[i], name)
		}
	}
}

func TestLoadMissingNATSURLUsesDefault(t *testing.T) {
	t.Setenv("PORT", "4105")
	t.Setenv("FORGE_NATS_URL", "")
	t.Setenv("FORGE_EVENTS_STREAMS", "build,runtime")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NATSURL != "nats://nats:4222" {
		t.Fatalf("NATSURL = %q, want default", cfg.NATSURL)
	}
	if len(cfg.Streams) != 2 || cfg.Streams[0] != "build" || cfg.Streams[1] != "runtime" {
		t.Fatalf("Streams = %#v, want [build runtime]", cfg.Streams)
	}
}

func TestLoadInvalidPortFailsFast(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	t.Setenv("FORGE_NATS_URL", "nats://127.0.0.1:4222")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid PORT")
	}
	if !contains(err.Error(), "PORT") {
		t.Fatalf("error = %q, want PORT mention", err.Error())
	}
}

func TestLoadInvalidStreamNameFailsFast(t *testing.T) {
	t.Setenv("PORT", "4105")
	t.Setenv("FORGE_EVENTS_STREAMS", "build,bad.name")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid stream name")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
