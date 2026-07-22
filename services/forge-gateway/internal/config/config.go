package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds env-based runtime settings for forge-gateway.
type Config struct {
	Port             int
	ServiceName      string
	ServiceVersion   string
	LogLevel         string
	Env              string
	AuthMode         string
	ShutdownGrace    time.Duration
	StaticRoutesPath string

	ControlURL        string
	RuntimeURL        string
	RouteSource       string
	RouteSyncInterval time.Duration
	HostPattern       string
	UpstreamHost      string
	SyncEnabled       bool
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
		name = "forge-gateway"
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

	graceRaw := strings.TrimSpace(os.Getenv("FORGE_SHUTDOWN_GRACE_SECONDS"))
	if graceRaw == "" {
		graceRaw = "10"
	}
	graceSecs, err := strconv.Atoi(graceRaw)
	if err != nil || graceSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got %q", graceRaw)
	}

	controlURL := strings.TrimSpace(os.Getenv("FORGE_CONTROL_URL"))
	runtimeURL := strings.TrimSpace(os.Getenv("FORGE_RUNTIME_URL"))

	routeSource := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_ROUTE_SOURCE")))
	if routeSource == "" {
		routeSource = "control"
	}
	switch routeSource {
	case "control", "runtime":
	default:
		return Config{}, fmt.Errorf("FORGE_ROUTE_SOURCE must be control|runtime, got %q", routeSource)
	}

	syncRaw := strings.TrimSpace(os.Getenv("FORGE_ROUTE_SYNC_INTERVAL_SECONDS"))
	if syncRaw == "" {
		syncRaw = "10"
	}
	syncSecs, err := strconv.Atoi(syncRaw)
	if err != nil || syncSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_ROUTE_SYNC_INTERVAL_SECONDS must be a non-negative integer, got %q", syncRaw)
	}

	hostPattern := strings.TrimSpace(os.Getenv("FORGE_HOST_PATTERN"))
	if hostPattern == "" {
		hostPattern = "{service}.{project}.demo.localhost"
	}

	upstreamHost := strings.TrimSpace(os.Getenv("FORGE_UPSTREAM_HOST"))
	if upstreamHost == "" {
		upstreamHost = "127.0.0.1"
	}

	// Sync is enabled when at least one platform URL is configured.
	// control source needs Control; runtime source needs both (metadata + state).
	syncEnabled := false
	switch routeSource {
	case "control":
		syncEnabled = controlURL != ""
	case "runtime":
		syncEnabled = controlURL != "" && runtimeURL != ""
	}

	return Config{
		Port:              port,
		ServiceName:       name,
		ServiceVersion:    version,
		LogLevel:          level,
		Env:               env,
		AuthMode:          authMode,
		ShutdownGrace:     time.Duration(graceSecs) * time.Second,
		StaticRoutesPath:  strings.TrimSpace(os.Getenv("FORGE_GATEWAY_STATIC_ROUTES")),
		ControlURL:        controlURL,
		RuntimeURL:        runtimeURL,
		RouteSource:       routeSource,
		RouteSyncInterval: time.Duration(syncSecs) * time.Second,
		HostPattern:       hostPattern,
		UpstreamHost:      upstreamHost,
		SyncEnabled:       syncEnabled,
	}, nil
}
