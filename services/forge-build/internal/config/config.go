package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds env-based runtime settings for forge-build.
type Config struct {
	Port           int
	ServiceName    string
	ServiceVersion string
	LogLevel       string
	Env            string
	AuthMode       string
	DockerHost     string
	WorkspaceDir   string
	ShutdownGrace  time.Duration

	DockerStartupRetries    int
	DockerStartupRetryDelay time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	portRaw := strings.TrimSpace(os.Getenv("PORT"))
	if portRaw == "" {
		return Config{}, fmt.Errorf("PORT is required")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("PORT must be an integer 1–65535, got %q", portRaw)
	}

	level := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_LOG_LEVEL")))
	if level == "" {
		level = "info"
	}
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("FORGE_LOG_LEVEL must be debug|info|warn|error, got %q", level)
	}

	name := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if name == "" {
		name = "forge-build"
	}
	version := strings.TrimSpace(os.Getenv("FORGE_SERVICE_VERSION"))
	if version == "" {
		version = "0.1.0"
	}
	env := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if env == "" {
		env = "development"
	}
	authMode := strings.TrimSpace(os.Getenv("FORGE_AUTH_MODE"))
	if authMode == "" {
		authMode = "dev"
	}

	dockerHost := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if dockerHost == "" {
		dockerHost = "unix:///var/run/docker.sock"
	}

	workspaceDir := strings.TrimSpace(os.Getenv("FORGE_BUILD_WORKSPACE_DIR"))
	if workspaceDir == "" {
		workspaceDir = "/workspace"
	}
	workspaceDir = filepath.Clean(workspaceDir)
	if !filepath.IsAbs(workspaceDir) {
		return Config{}, fmt.Errorf("FORGE_BUILD_WORKSPACE_DIR must be an absolute path, got %q", workspaceDir)
	}

	graceRaw := strings.TrimSpace(os.Getenv("FORGE_SHUTDOWN_GRACE_SECONDS"))
	if graceRaw == "" {
		graceRaw = "10"
	}
	graceSecs, err := strconv.Atoi(graceRaw)
	if err != nil || graceSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got %q", graceRaw)
	}

	retriesRaw := strings.TrimSpace(os.Getenv("FORGE_DOCKER_STARTUP_RETRIES"))
	if retriesRaw == "" {
		retriesRaw = "5"
	}
	retries, err := strconv.Atoi(retriesRaw)
	if err != nil || retries < 0 {
		return Config{}, fmt.Errorf("FORGE_DOCKER_STARTUP_RETRIES must be a non-negative integer, got %q", retriesRaw)
	}

	delayRaw := strings.TrimSpace(os.Getenv("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS"))
	if delayRaw == "" {
		delayRaw = "500"
	}
	delayMs, err := strconv.Atoi(delayRaw)
	if err != nil || delayMs < 0 {
		return Config{}, fmt.Errorf("FORGE_DOCKER_STARTUP_RETRY_DELAY_MS must be a non-negative integer, got %q", delayRaw)
	}

	return Config{
		Port:                    port,
		ServiceName:             name,
		ServiceVersion:          version,
		LogLevel:                level,
		Env:                     env,
		AuthMode:                authMode,
		DockerHost:              dockerHost,
		WorkspaceDir:            workspaceDir,
		ShutdownGrace:           time.Duration(graceSecs) * time.Second,
		DockerStartupRetries:    retries,
		DockerStartupRetryDelay: time.Duration(delayMs) * time.Millisecond,
	}, nil
}
