package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_SERVICE_NAME", "")
	t.Setenv("FORGE_SERVICE_VERSION", "")
	t.Setenv("FORGE_LOG_LEVEL", "")
	t.Setenv("FORGE_ENV", "")
	t.Setenv("FORGE_AUTH_MODE", "")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.ServiceName != "forge-gateway" {
		t.Fatalf("ServiceName = %q, want forge-gateway", cfg.ServiceName)
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
	if cfg.AuthMode != "dev" {
		t.Fatalf("AuthMode = %q, want dev", cfg.AuthMode)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 10s", cfg.ShutdownGrace)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	cases := []string{"", "abc", "0", "-1", "70000"}
	for _, port := range cases {
		t.Run("PORT="+port, func(t *testing.T) {
			t.Setenv("PORT", port)
			if _, err := Load(); err == nil {
				t.Fatal("expected error for invalid PORT")
			}
		})
	}
}

func TestLoadCustomValues(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("FORGE_SERVICE_NAME", "gw")
	t.Setenv("FORGE_SERVICE_VERSION", "1.2.3")
	t.Setenv("FORGE_LOG_LEVEL", "DEBUG")
	t.Setenv("FORGE_ENV", "test")
	t.Setenv("FORGE_AUTH_MODE", "dev")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9090 || cfg.ServiceName != "gw" || cfg.ServiceVersion != "1.2.3" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.ShutdownGrace != 5*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 5s", cfg.ShutdownGrace)
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_LOG_LEVEL", "verbose")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid FORGE_LOG_LEVEL")
	}
}
