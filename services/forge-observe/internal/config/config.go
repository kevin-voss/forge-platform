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
	AlertmanagerURL  string
	BackendTimeout   time.Duration
	RequiredBackends []BackendName
	LogQueryMaxLimit int
	LogQueryMaxRange time.Duration
	AuthMode         string
	IdentityURL      string
	AuthzCacheTTLS   int
	// Alert threshold documentation defaults (rules are provisioned as code).
	AlertServiceDownFor     time.Duration
	AlertErrorRateThreshold float64
	AlertErrorRateFor       time.Duration
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
	amURL, err := parseBackendURL("FORGE_ALERTMANAGER_URL", os.Getenv("FORGE_ALERTMANAGER_URL"), "http://alertmanager:9093")
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

	maxLimit, err := parsePositiveInt("FORGE_LOG_QUERY_MAX_LIMIT", os.Getenv("FORGE_LOG_QUERY_MAX_LIMIT"), 1000)
	if err != nil {
		return Config{}, err
	}
	maxRangeH, err := parsePositiveInt("FORGE_LOG_QUERY_MAX_RANGE_H", os.Getenv("FORGE_LOG_QUERY_MAX_RANGE_H"), 24)
	if err != nil {
		return Config{}, err
	}

	authMode := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTH_MODE")))
	if authMode == "" {
		authMode = "dev"
	}
	switch authMode {
	case "dev", "enforce":
	default:
		return Config{}, fmt.Errorf("FORGE_AUTH_MODE must be enforce|dev, got %q", authMode)
	}
	identityURL, err := parseBackendURL("FORGE_IDENTITY_URL", os.Getenv("FORGE_IDENTITY_URL"), "http://forge-identity:4002")
	if err != nil {
		return Config{}, err
	}
	authzTTL, err := parsePositiveInt("FORGE_AUTHZ_CACHE_TTL_S", os.Getenv("FORGE_AUTHZ_CACHE_TTL_S"), 10)
	if err != nil {
		return Config{}, err
	}

	serviceDownFor, err := parseDurationEnv("FORGE_ALERT_SERVICE_DOWN_FOR", os.Getenv("FORGE_ALERT_SERVICE_DOWN_FOR"), 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	errorRateFor, err := parseDurationEnv("FORGE_ALERT_ERROR_RATE_FOR", os.Getenv("FORGE_ALERT_ERROR_RATE_FOR"), 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	errorRateThreshold, err := parseFloatEnv("FORGE_ALERT_ERROR_RATE_THRESHOLD", os.Getenv("FORGE_ALERT_ERROR_RATE_THRESHOLD"), 0.05)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Port:                    port,
		ServiceName:             name,
		ServiceVersion:          version,
		LogLevel:                level,
		Env:                     env,
		ShutdownGrace:           time.Duration(graceSecs) * time.Second,
		LokiURL:                 lokiURL,
		TempoURL:                tempoURL,
		PrometheusURL:           promURL,
		AlertmanagerURL:         amURL,
		BackendTimeout:          time.Duration(timeoutMS) * time.Millisecond,
		RequiredBackends:        required,
		LogQueryMaxLimit:        maxLimit,
		LogQueryMaxRange:        time.Duration(maxRangeH) * time.Hour,
		AuthMode:                authMode,
		IdentityURL:             identityURL,
		AuthzCacheTTLS:          authzTTL,
		AlertServiceDownFor:     serviceDownFor,
		AlertErrorRateThreshold: errorRateThreshold,
		AlertErrorRateFor:       errorRateFor,
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

func parseDurationEnv(name, raw string, def time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration (e.g. 30s), got %q", name, raw)
	}
	return d, nil
}

func parseFloatEnv(name, raw string, def float64) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || v >= 1 {
		return 0, fmt.Errorf("%s must be a float in (0,1), got %q", name, raw)
	}
	return v, nil
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
