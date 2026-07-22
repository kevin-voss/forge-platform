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
	Port             int
	ServiceName      string
	ServiceVersion   string
	LogLevel         string
	Env              string
	AuthMode         string
	DockerHost       string
	WorkspaceDir     string
	DefaultForgeYAML string
	ShutdownGrace    time.Duration

	BuildTimeout   time.Duration
	MaxConcurrency int
	LogBufferLines int

	Registry         string
	ImageNamePattern string
	DefaultProject   string
	PushLatest       bool
	PushRetries      int

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

	defaultForgeYAML := strings.TrimSpace(os.Getenv("FORGE_DEFAULT_FORGE_YAML"))
	if defaultForgeYAML == "" {
		defaultForgeYAML = "forge.yaml"
	}
	if filepath.IsAbs(defaultForgeYAML) || strings.Contains(defaultForgeYAML, "..") {
		return Config{}, fmt.Errorf("FORGE_DEFAULT_FORGE_YAML must be a relative path without '..', got %q", defaultForgeYAML)
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

	timeoutRaw := strings.TrimSpace(os.Getenv("FORGE_BUILD_TIMEOUT_SECONDS"))
	if timeoutRaw == "" {
		timeoutRaw = "600"
	}
	timeoutSecs, err := strconv.Atoi(timeoutRaw)
	if err != nil || timeoutSecs < 1 {
		return Config{}, fmt.Errorf("FORGE_BUILD_TIMEOUT_SECONDS must be a positive integer, got %q", timeoutRaw)
	}

	concurrencyRaw := strings.TrimSpace(os.Getenv("FORGE_BUILD_MAX_CONCURRENCY"))
	if concurrencyRaw == "" {
		concurrencyRaw = "2"
	}
	concurrency, err := strconv.Atoi(concurrencyRaw)
	if err != nil || concurrency < 1 {
		return Config{}, fmt.Errorf("FORGE_BUILD_MAX_CONCURRENCY must be a positive integer, got %q", concurrencyRaw)
	}

	logLinesRaw := strings.TrimSpace(os.Getenv("FORGE_BUILD_LOG_BUFFER_LINES"))
	if logLinesRaw == "" {
		logLinesRaw = "5000"
	}
	logLines, err := strconv.Atoi(logLinesRaw)
	if err != nil || logLines < 1 {
		return Config{}, fmt.Errorf("FORGE_BUILD_LOG_BUFFER_LINES must be a positive integer, got %q", logLinesRaw)
	}

	registryHost := strings.TrimSpace(os.Getenv("FORGE_REGISTRY"))
	if registryHost == "" {
		registryHost = "localhost:5000"
	}
	if strings.Contains(registryHost, "://") {
		return Config{}, fmt.Errorf("FORGE_REGISTRY must be host[:port] without scheme, got %q", registryHost)
	}

	imagePattern := strings.TrimSpace(os.Getenv("FORGE_IMAGE_NAME_PATTERN"))
	if imagePattern == "" {
		imagePattern = "{project}-{service}"
	}
	if !strings.Contains(imagePattern, "{service}") {
		return Config{}, fmt.Errorf("FORGE_IMAGE_NAME_PATTERN must contain {service}, got %q", imagePattern)
	}

	defaultProject := strings.TrimSpace(os.Getenv("FORGE_DEFAULT_PROJECT"))

	pushLatest := true
	if raw := strings.TrimSpace(os.Getenv("FORGE_PUSH_LATEST")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("FORGE_PUSH_LATEST must be a boolean, got %q", raw)
		}
		pushLatest = v
	}

	pushRetriesRaw := strings.TrimSpace(os.Getenv("FORGE_PUSH_RETRIES"))
	if pushRetriesRaw == "" {
		pushRetriesRaw = "3"
	}
	pushRetries, err := strconv.Atoi(pushRetriesRaw)
	if err != nil || pushRetries < 0 {
		return Config{}, fmt.Errorf("FORGE_PUSH_RETRIES must be a non-negative integer, got %q", pushRetriesRaw)
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
		DefaultForgeYAML:        defaultForgeYAML,
		ShutdownGrace:           time.Duration(graceSecs) * time.Second,
		BuildTimeout:            time.Duration(timeoutSecs) * time.Second,
		MaxConcurrency:          concurrency,
		LogBufferLines:          logLines,
		Registry:                registryHost,
		ImageNamePattern:        imagePattern,
		DefaultProject:          defaultProject,
		PushLatest:              pushLatest,
		PushRetries:             pushRetries,
		DockerStartupRetries:    retries,
		DockerStartupRetryDelay: time.Duration(delayMs) * time.Millisecond,
	}, nil
}
