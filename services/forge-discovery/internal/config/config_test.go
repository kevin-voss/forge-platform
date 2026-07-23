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
	t.Setenv("FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT", "")
	t.Setenv("FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS", "")
	t.Setenv("FORGE_DISCOVERY_REAP_AFTER_SECONDS", "")
	t.Setenv("FORGE_DISCOVERY_NODE_WATCH_RESYNC_SECONDS", "")
	t.Setenv("FORGE_DISCOVERY_WATCH_BUFFER_SIZE", "")
	t.Setenv("FORGE_DISCOVERY_WATCH_MAX_CONNECTIONS", "")
	t.Setenv("FORGE_DISCOVERY_WATCH_HEARTBEAT_SECONDS", "")

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
	if cfg.LeaseSecondsDefault != 20 {
		t.Fatalf("LeaseSecondsDefault = %d", cfg.LeaseSecondsDefault)
	}
	if cfg.SweepInterval != 5*time.Second {
		t.Fatalf("SweepInterval = %v", cfg.SweepInterval)
	}
	if cfg.ReapAfter != 300*time.Second {
		t.Fatalf("ReapAfter = %v", cfg.ReapAfter)
	}
	if cfg.WatchBufferSize != 500 || cfg.WatchMaxConnections != 1000 || cfg.WatchHeartbeat != 15*time.Second {
		t.Fatalf("watch cfg = %+v", cfg)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}
