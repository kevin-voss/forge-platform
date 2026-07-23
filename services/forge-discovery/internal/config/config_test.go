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
	t.Setenv("FORGE_DATABASE_URL", "")
	t.Setenv("FORGE_DATABASE_SCHEMA", "")
	t.Setenv("FORGE_DATABASE_POOL_MAX", "")
	t.Setenv("FORGE_DATABASE_MIGRATE_ON_START", "")
	t.Setenv("FORGE_CONTROL_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d", cfg.Port)
	}
	if cfg.ServiceName != "forge-discovery" {
		t.Fatalf("ServiceName = %q", cfg.ServiceName)
	}
	if cfg.DatabaseSchema != "discovery" {
		t.Fatalf("DatabaseSchema = %q", cfg.DatabaseSchema)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("ShutdownGrace = %v", cfg.ShutdownGrace)
	}
	if !cfg.DatabaseMigrateOnStart {
		t.Fatal("expected migrate on start")
	}
}

func TestLoadInvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}
