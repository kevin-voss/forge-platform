package config

import (
	"path/filepath"
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
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("FORGE_BUILD_WORKSPACE_DIR", "")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRIES", "")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.ServiceName != "forge-build" {
		t.Fatalf("ServiceName = %q, want forge-build", cfg.ServiceName)
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
	if cfg.DockerHost != "unix:///var/run/docker.sock" {
		t.Fatalf("DockerHost = %q", cfg.DockerHost)
	}
	if cfg.WorkspaceDir != "/workspace" {
		t.Fatalf("WorkspaceDir = %q, want /workspace", cfg.WorkspaceDir)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 10s", cfg.ShutdownGrace)
	}
	if cfg.DockerStartupRetries != 5 {
		t.Fatalf("DockerStartupRetries = %d, want 5", cfg.DockerStartupRetries)
	}
	if cfg.DockerStartupRetryDelay != 500*time.Millisecond {
		t.Fatalf("DockerStartupRetryDelay = %v, want 500ms", cfg.DockerStartupRetryDelay)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	cases := []string{"", "abc", "0", "-1", "70000"}
	for _, port := range cases {
		t.Run("PORT="+port, func(t *testing.T) {
			t.Setenv("PORT", port)
			t.Setenv("FORGE_BUILD_WORKSPACE_DIR", "/workspace")
			if _, err := Load(); err == nil {
				t.Fatal("expected error for invalid PORT")
			}
		})
	}
}

func TestLoadInvalidWorkspaceDir(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_BUILD_WORKSPACE_DIR", "relative/workspace")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for relative workspace dir")
	}
}

func TestLoadCustomValues(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "builds")
	t.Setenv("PORT", "9090")
	t.Setenv("FORGE_SERVICE_NAME", "build")
	t.Setenv("FORGE_SERVICE_VERSION", "1.2.3")
	t.Setenv("FORGE_LOG_LEVEL", "DEBUG")
	t.Setenv("FORGE_ENV", "test")
	t.Setenv("FORGE_AUTH_MODE", "dev")
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	t.Setenv("FORGE_BUILD_WORKSPACE_DIR", ws)
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "5")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRIES", "2")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS", "100")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9090 || cfg.ServiceName != "build" || cfg.ServiceVersion != "1.2.3" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.DockerHost != "unix:///tmp/docker.sock" {
		t.Fatalf("DockerHost = %q", cfg.DockerHost)
	}
	if cfg.WorkspaceDir != filepath.Clean(ws) {
		t.Fatalf("WorkspaceDir = %q, want %q", cfg.WorkspaceDir, filepath.Clean(ws))
	}
	if cfg.ShutdownGrace != 5*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 5s", cfg.ShutdownGrace)
	}
	if cfg.DockerStartupRetries != 2 || cfg.DockerStartupRetryDelay != 100*time.Millisecond {
		t.Fatalf("docker startup: retries=%d delay=%v", cfg.DockerStartupRetries, cfg.DockerStartupRetryDelay)
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_LOG_LEVEL", "verbose")
	t.Setenv("FORGE_BUILD_WORKSPACE_DIR", "/workspace")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid FORGE_LOG_LEVEL")
	}
}
