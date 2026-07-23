package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultStreams is the platform JetStream stream set for epic 11.
var DefaultStreams = []string{"build", "deployment", "runtime", "application", "agent"}

// Config holds env-based runtime settings for forge-events.
type Config struct {
	Port                 int
	ServiceName          string
	ServiceVersion       string
	LogLevel             string
	Env                  string
	ShutdownGrace        time.Duration
	NATSURL              string
	Streams              []string
	EventMaxBytes        int
	ConsumeMaxBatch      int
	ConsumeWait          time.Duration
	DefaultAckWaitS      int
	DefaultMaxDeliveries int
	AckTokenTTLS         int
	DLQEnabled           bool
	DLQRetentionDays     int
	EventSchemaDir       string
	SchemaValidation     string // strict|warn
	DedupWindowS         int
	SeenStoreTTLS        int
	EventsDBURL          string
	AuthMode             string // enforce|dev
	IdentityURL          string
	IntrospectCacheTTLS  int
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	portRaw := strings.TrimSpace(os.Getenv("PORT"))
	if portRaw == "" {
		portRaw = "4105"
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
		name = "forge-events"
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

	natsURL := strings.TrimSpace(os.Getenv("FORGE_NATS_URL"))
	if natsURL == "" {
		natsURL = "nats://nats:4222"
	}

	streams, err := parseStreams(os.Getenv("FORGE_EVENTS_STREAMS"))
	if err != nil {
		return Config{}, err
	}

	maxBytes, err := parsePositiveInt("FORGE_EVENT_MAX_BYTES", os.Getenv("FORGE_EVENT_MAX_BYTES"), 256*1024)
	if err != nil {
		return Config{}, err
	}
	maxBatch, err := parsePositiveInt("FORGE_CONSUME_MAX_BATCH", os.Getenv("FORGE_CONSUME_MAX_BATCH"), 100)
	if err != nil {
		return Config{}, err
	}
	waitMS, err := parseNonNegativeInt("FORGE_CONSUME_WAIT_MS", os.Getenv("FORGE_CONSUME_WAIT_MS"), 2000)
	if err != nil {
		return Config{}, err
	}
	ackWaitS, err := parsePositiveInt("FORGE_DEFAULT_ACK_WAIT_S", os.Getenv("FORGE_DEFAULT_ACK_WAIT_S"), 30)
	if err != nil {
		return Config{}, err
	}
	maxDeliveries, err := parsePositiveInt("FORGE_DEFAULT_MAX_DELIVERIES", os.Getenv("FORGE_DEFAULT_MAX_DELIVERIES"), 5)
	if err != nil {
		return Config{}, err
	}
	ackTokenTTL, err := parsePositiveInt("FORGE_ACK_TOKEN_TTL_S", os.Getenv("FORGE_ACK_TOKEN_TTL_S"), 0)
	if err != nil {
		return Config{}, err
	}
	if ackTokenTTL == 0 {
		// Token validity window must cover at least ack_wait.
		ackTokenTTL = ackWaitS
		if ackTokenTTL < 60 {
			ackTokenTTL = 60
		}
	}
	if ackTokenTTL < ackWaitS {
		return Config{}, fmt.Errorf("FORGE_ACK_TOKEN_TTL_S (%d) must be >= FORGE_DEFAULT_ACK_WAIT_S (%d)", ackTokenTTL, ackWaitS)
	}

	dlqEnabled, err := parseBool("FORGE_DLQ_ENABLED", os.Getenv("FORGE_DLQ_ENABLED"), true)
	if err != nil {
		return Config{}, err
	}
	dlqRetentionDays, err := parsePositiveInt("FORGE_DLQ_RETENTION_DAYS", os.Getenv("FORGE_DLQ_RETENTION_DAYS"), 7)
	if err != nil {
		return Config{}, err
	}

	schemaDir := strings.TrimSpace(os.Getenv("FORGE_EVENT_SCHEMA_DIR"))
	if schemaDir == "" {
		schemaDir = "/contracts/events"
	}
	schemaValidation := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_SCHEMA_VALIDATION")))
	if schemaValidation == "" {
		schemaValidation = "strict"
	}
	switch schemaValidation {
	case "strict", "warn":
	default:
		return Config{}, fmt.Errorf("FORGE_SCHEMA_VALIDATION must be strict|warn, got %q", schemaValidation)
	}

	dedupWindowS, err := parsePositiveInt("FORGE_DEDUP_WINDOW_S", os.Getenv("FORGE_DEDUP_WINDOW_S"), 120)
	if err != nil {
		return Config{}, err
	}
	seenTTL, err := parsePositiveInt("FORGE_SEEN_STORE_TTL_S", os.Getenv("FORGE_SEEN_STORE_TTL_S"), 86400)
	if err != nil {
		return Config{}, err
	}
	dbURL := strings.TrimSpace(os.Getenv("FORGE_EVENTS_DB_URL"))
	authMode := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTH_MODE")))
	if authMode == "" {
		authMode = "dev"
	}
	switch authMode {
	case "dev", "enforce":
	default:
		return Config{}, fmt.Errorf("FORGE_AUTH_MODE must be enforce|dev, got %q", authMode)
	}
	identityURL := strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL"))
	if identityURL == "" {
		identityURL = "http://forge-identity:8080"
	}
	introspectTTL, err := parsePositiveInt("FORGE_INTROSPECT_CACHE_TTL_S", os.Getenv("FORGE_INTROSPECT_CACHE_TTL_S"), 10)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Port:                 port,
		ServiceName:          name,
		ServiceVersion:       version,
		LogLevel:             level,
		Env:                  env,
		ShutdownGrace:        time.Duration(graceSecs) * time.Second,
		NATSURL:              natsURL,
		Streams:              streams,
		EventMaxBytes:        maxBytes,
		ConsumeMaxBatch:      maxBatch,
		ConsumeWait:          time.Duration(waitMS) * time.Millisecond,
		DefaultAckWaitS:      ackWaitS,
		DefaultMaxDeliveries: maxDeliveries,
		AckTokenTTLS:         ackTokenTTL,
		DLQEnabled:           dlqEnabled,
		DLQRetentionDays:     dlqRetentionDays,
		EventSchemaDir:       schemaDir,
		SchemaValidation:     schemaValidation,
		DedupWindowS:         dedupWindowS,
		SeenStoreTTLS:        seenTTL,
		EventsDBURL:          dbURL,
		AuthMode:             authMode,
		IdentityURL:          identityURL,
		IntrospectCacheTTLS:  introspectTTL,
	}, nil
}

func parseBool(name, raw string, def bool) (bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return def, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true|false, got %q", name, raw)
	}
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

func parseNonNegativeInt(name, raw string, def int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer, got %q", name, raw)
	}
	return n, nil
}

func parseStreams(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		out := make([]string, len(DefaultStreams))
		copy(out, DefaultStreams)
		return out, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			continue
		}
		if !isValidStreamName(name) {
			return nil, fmt.Errorf("FORGE_EVENTS_STREAMS contains invalid stream name %q", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("FORGE_EVENTS_STREAMS must list at least one stream")
	}
	return out, nil
}

func isValidStreamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
