package config

import (
	"strings"
	"testing"
	"time"
)

func clearObserveEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT",
		"FORGE_SERVICE_NAME",
		"FORGE_SERVICE_VERSION",
		"FORGE_LOG_LEVEL",
		"FORGE_ENV",
		"FORGE_SHUTDOWN_GRACE_SECONDS",
		"FORGE_LOKI_URL",
		"FORGE_TEMPO_URL",
		"FORGE_PROMETHEUS_URL",
		"FORGE_BACKEND_TIMEOUT_MS",
		"FORGE_OBSERVE_READY_REQUIRE_BACKENDS",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearObserveEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 4106 {
		t.Fatalf("Port = %d, want 4106", cfg.Port)
	}
	if cfg.ServiceName != "forge-observe" {
		t.Fatalf("ServiceName = %q", cfg.ServiceName)
	}
	if cfg.LokiURL != "http://loki:3100" {
		t.Fatalf("LokiURL = %q", cfg.LokiURL)
	}
	if cfg.TempoURL != "http://tempo:3200" {
		t.Fatalf("TempoURL = %q", cfg.TempoURL)
	}
	if cfg.PrometheusURL != "http://prometheus:9090" {
		t.Fatalf("PrometheusURL = %q", cfg.PrometheusURL)
	}
	if cfg.BackendTimeout != 2*time.Second {
		t.Fatalf("BackendTimeout = %v, want 2s", cfg.BackendTimeout)
	}
	if len(cfg.RequiredBackends) != 3 {
		t.Fatalf("RequiredBackends = %v, want 3", cfg.RequiredBackends)
	}
}

func TestLoadCustomBackendURLs(t *testing.T) {
	clearObserveEnv(t)
	t.Setenv("FORGE_LOKI_URL", "http://127.0.0.1:3003")
	t.Setenv("FORGE_TEMPO_URL", "http://127.0.0.1:3002/")
	t.Setenv("FORGE_PROMETHEUS_URL", "http://127.0.0.1:3001")
	t.Setenv("FORGE_OBSERVE_READY_REQUIRE_BACKENDS", "loki,prometheus")
	t.Setenv("FORGE_BACKEND_TIMEOUT_MS", "1500")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LokiURL != "http://127.0.0.1:3003" {
		t.Fatalf("LokiURL = %q", cfg.LokiURL)
	}
	if cfg.TempoURL != "http://127.0.0.1:3002" {
		t.Fatalf("TempoURL = %q (trailing slash should be trimmed)", cfg.TempoURL)
	}
	if cfg.BackendTimeout != 1500*time.Millisecond {
		t.Fatalf("BackendTimeout = %v", cfg.BackendTimeout)
	}
	if len(cfg.RequiredBackends) != 2 || cfg.RequiredBackends[0] != BackendLoki || cfg.RequiredBackends[1] != BackendPrometheus {
		t.Fatalf("RequiredBackends = %v", cfg.RequiredBackends)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	clearObserveEnv(t)
	t.Setenv("PORT", "not-a-port")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("expected PORT error, got %v", err)
	}
}

func TestLoadInvalidBackendURL(t *testing.T) {
	clearObserveEnv(t)
	t.Setenv("FORGE_LOKI_URL", "not-a-url")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "FORGE_LOKI_URL") {
		t.Fatalf("expected FORGE_LOKI_URL error, got %v", err)
	}
}

func TestLoadUnknownRequiredBackend(t *testing.T) {
	clearObserveEnv(t)
	t.Setenv("FORGE_OBSERVE_READY_REQUIRE_BACKENDS", "loki,graphite")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "graphite") {
		t.Fatalf("expected unknown backend error, got %v", err)
	}
}
