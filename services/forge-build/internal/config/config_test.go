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
	t.Setenv("FORGE_DEFAULT_FORGE_YAML", "")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRIES", "")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS", "")
	t.Setenv("FORGE_BUILD_TIMEOUT_SECONDS", "")
	t.Setenv("FORGE_BUILD_MAX_CONCURRENCY", "")
	t.Setenv("FORGE_BUILD_LOG_BUFFER_LINES", "")
	t.Setenv("FORGE_REGISTRY", "")
	t.Setenv("FORGE_IMAGE_NAME_PATTERN", "")
	t.Setenv("FORGE_DEFAULT_PROJECT", "")
	t.Setenv("FORGE_PUSH_LATEST", "")
	t.Setenv("FORGE_PUSH_RETRIES", "")
	t.Setenv("FORGE_BUILD_STORE_DIR", "")
	t.Setenv("FORGE_BUILD_RETENTION_HOURS", "")
	t.Setenv("FORGE_BUILD_CLEANUP_ON_START", "")
	t.Setenv("FORGE_CONTROL_URL", "")
	t.Setenv("FORGE_BUILD_AUTO_DEPLOY_DEFAULT", "")
	t.Setenv("FORGE_CONTROL_RETRIES", "")
	t.Setenv("FORGE_CONTROL_RETRY_BACKOFF_MS", "")
	t.Setenv("FORGE_CONTROL_TIMEOUT_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.DefaultForgeYAML != "forge.yaml" {
		t.Fatalf("DefaultForgeYAML = %q, want forge.yaml", cfg.DefaultForgeYAML)
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
	if cfg.BuildTimeout != 600*time.Second {
		t.Fatalf("BuildTimeout = %v, want 600s", cfg.BuildTimeout)
	}
	if cfg.MaxConcurrency != 2 {
		t.Fatalf("MaxConcurrency = %d, want 2", cfg.MaxConcurrency)
	}
	if cfg.LogBufferLines != 5000 {
		t.Fatalf("LogBufferLines = %d, want 5000", cfg.LogBufferLines)
	}
	if cfg.Registry != "localhost:5000" {
		t.Fatalf("Registry = %q", cfg.Registry)
	}
	if cfg.ImageNamePattern != "{project}-{service}" {
		t.Fatalf("ImageNamePattern = %q", cfg.ImageNamePattern)
	}
	if cfg.DefaultProject != "" {
		t.Fatalf("DefaultProject = %q", cfg.DefaultProject)
	}
	if !cfg.PushLatest {
		t.Fatal("PushLatest want true")
	}
	if cfg.PushRetries != 3 {
		t.Fatalf("PushRetries = %d", cfg.PushRetries)
	}
	if cfg.StoreDir != "/var/lib/forge-build" {
		t.Fatalf("StoreDir = %q", cfg.StoreDir)
	}
	if cfg.Retention != 72*time.Hour {
		t.Fatalf("Retention = %v", cfg.Retention)
	}
	if !cfg.CleanupOnStart {
		t.Fatal("CleanupOnStart want true")
	}
	if cfg.ControlURL != "" {
		t.Fatalf("ControlURL = %q, want empty", cfg.ControlURL)
	}
	if cfg.AutoDeployDefault {
		t.Fatal("AutoDeployDefault want false")
	}
	if cfg.ControlRetries != 5 {
		t.Fatalf("ControlRetries = %d", cfg.ControlRetries)
	}
	if cfg.ControlRetryBackoff != 200*time.Millisecond {
		t.Fatalf("ControlRetryBackoff = %v", cfg.ControlRetryBackoff)
	}
	if cfg.ControlTimeout != 10*time.Second {
		t.Fatalf("ControlTimeout = %v", cfg.ControlTimeout)
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
	t.Setenv("FORGE_DEFAULT_FORGE_YAML", "deploy/forge.yaml")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "5")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRIES", "2")
	t.Setenv("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS", "100")
	t.Setenv("FORGE_BUILD_TIMEOUT_SECONDS", "120")
	t.Setenv("FORGE_BUILD_MAX_CONCURRENCY", "3")
	t.Setenv("FORGE_BUILD_LOG_BUFFER_LINES", "100")
	t.Setenv("FORGE_REGISTRY", "localhost:5000")
	t.Setenv("FORGE_IMAGE_NAME_PATTERN", "{service}")
	t.Setenv("FORGE_DEFAULT_PROJECT", "acme")
	t.Setenv("FORGE_PUSH_LATEST", "false")
	t.Setenv("FORGE_PUSH_RETRIES", "5")
	storeDir := filepath.Join(t.TempDir(), "store")
	t.Setenv("FORGE_BUILD_STORE_DIR", storeDir)
	t.Setenv("FORGE_BUILD_RETENTION_HOURS", "24")
	t.Setenv("FORGE_BUILD_CLEANUP_ON_START", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9090 || cfg.ServiceName != "build" || cfg.ServiceVersion != "1.2.3" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if cfg.Registry != "localhost:5000" || cfg.ImageNamePattern != "{service}" || cfg.DefaultProject != "acme" {
		t.Fatalf("registry cfg: %+v", cfg)
	}
	if cfg.PushLatest || cfg.PushRetries != 5 {
		t.Fatalf("push cfg: latest=%v retries=%d", cfg.PushLatest, cfg.PushRetries)
	}
	if cfg.StoreDir != filepath.Clean(storeDir) || cfg.Retention != 24*time.Hour || cfg.CleanupOnStart {
		t.Fatalf("store cfg: dir=%q retention=%v cleanup=%v", cfg.StoreDir, cfg.Retention, cfg.CleanupOnStart)
	}
	if cfg.DefaultForgeYAML != "deploy/forge.yaml" {
		t.Fatalf("DefaultForgeYAML = %q", cfg.DefaultForgeYAML)
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
	if cfg.BuildTimeout != 120*time.Second || cfg.MaxConcurrency != 3 || cfg.LogBufferLines != 100 {
		t.Fatalf("build cfg: timeout=%v concurrency=%d lines=%d", cfg.BuildTimeout, cfg.MaxConcurrency, cfg.LogBufferLines)
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

func TestLoadInvalidDefaultForgeYAML(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_BUILD_WORKSPACE_DIR", "/workspace")
	t.Setenv("FORGE_DEFAULT_FORGE_YAML", "../forge.yaml")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for path-traversal FORGE_DEFAULT_FORGE_YAML")
	}
}
