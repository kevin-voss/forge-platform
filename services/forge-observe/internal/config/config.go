package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// BackendName identifies a telemetry backend Observe queries.
type BackendName string

const (
	BackendLoki       BackendName = "loki"
	BackendTempo      BackendName = "tempo"
	BackendPrometheus BackendName = "prometheus"
)

// DefaultRequiredBackends is the default readiness gate set.
var DefaultRequiredBackends = []BackendName{BackendLoki, BackendTempo, BackendPrometheus}

// Config holds env-based runtime settings for forge-observe.
type Config struct {
	Port             int
	ServiceName      string
	ServiceVersion   string
	LogLevel         string
	Env              string
	ShutdownGrace    time.Duration
	LokiURL          string
	TempoURL         string
	PrometheusURL    string
	BackendTimeout   time.Duration
	RequiredBackends []BackendName
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	portRaw := strings.TrimSpace(os.Getenv("PORT"))
	if portRaw == "" {
		portRaw = "4106"
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
		name = "forge-observe"
	}
	version := strings.TrimSpace(os.Getenv("FORGE_SERVICE_VERSION"))
	if version == "" {
		version = "0.1.0"
	}
	env := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if env == "" {
		env = "development"
	}

	graceRaw := strings.TrimSpace(os.Getenv("FORGE_SHUTDOWN_GRACE_SECONDS"))
	if graceRaw == "" {
		graceRaw = "10"
	}
	graceSecs, err := strconv.Atoi(graceRaw)
	if err != nil || graceSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_SHUTDOWN_GRACE_SECONDS must be a non-negative integer, got %q", graceRaw)
	}

	lokiURL, err := parseBackendURL("FORGE_LOKI_URL", os.Getenv("FORGE_LOKI_URL"), "http://loki:3100")
	if err != nil {
		return Config{}, err
	}
	tempoURL, err := parseBackendURL("FORGE_TEMPO_URL", os.Getenv("FORGE_TEMPO_URL"), "http://tempo:3200")
	if err != nil {
		return Config{}, err
	}
	promURL, err := parseBackendURL("FORGE_PROMETHEUS_URL", os.Getenv("FORGE_PROMETHEUS_URL"), "http://prometheus:9090")
	if err != nil {
		return Config{}, err
	}

	timeoutMS, err := parsePositiveInt("FORGE_BACKEND_TIMEOUT_MS", os.Getenv("FORGE_BACKEND_TIMEOUT_MS"), 2000)
	if err != nil {
		return Config{}, err
	}

	required, err := parseRequiredBackends(os.Getenv("FORGE_OBSERVE_READY_REQUIRE_BACKENDS"))
	if err != nil {
		return Config{}, err
	}

	return Config{
		Port:             port,
		ServiceName:      name,
		ServiceVersion:   version,
		LogLevel:         level,
		Env:              env,
		ShutdownGrace:    time.Duration(graceSecs) * time.Second,
		LokiURL:          lokiURL,
		TempoURL:         tempoURL,
		PrometheusURL:    promURL,
		BackendTimeout:   time.Duration(timeoutMS) * time.Millisecond,
		RequiredBackends: required,
	}, nil
}

func parseBackendURL(name, raw, def string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = def
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%s must be an absolute http(s) URL, got %q", name, raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("%s must use http or https, got %q", name, raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

func parsePositiveInt(name, raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", name, raw)
	}
	return n, nil
}

func parseRequiredBackends(raw string) ([]BackendName, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		out := make([]BackendName, len(DefaultRequiredBackends))
		copy(out, DefaultRequiredBackends)
		return out, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]BackendName, 0, len(parts))
	seen := make(map[BackendName]struct{}, len(parts))
	for _, p := range parts {
		name := BackendName(strings.ToLower(strings.TrimSpace(p)))
		if name == "" {
			continue
		}
		switch name {
		case BackendLoki, BackendTempo, BackendPrometheus:
		default:
			return nil, fmt.Errorf("FORGE_OBSERVE_READY_REQUIRE_BACKENDS contains unknown backend %q (want loki|tempo|prometheus)", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("FORGE_OBSERVE_READY_REQUIRE_BACKENDS must list at least one backend")
	}
	return out, nil
}
