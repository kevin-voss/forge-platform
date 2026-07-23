package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type config struct {
	Port           int
	ServiceName    string
	ServiceVersion string
	LogLevel       string
	Env            string
	// EventsURL is the Forge Events base URL. Empty disables publish.
	EventsURL string
	// DatabaseURL from Secrets/Runtime injection. Empty → in-memory (local smoke).
	DatabaseURL string
	// ProductAuth enforce|dev. When enforce, Identity introspect is required.
	ProductAuth string
	IdentityURL string
	// Expected project id for token binding (optional; empty accepts any active token).
	ProjectID string
	// OTEL
	OTELEnabled  bool
	OTELEndpoint string
	// Storage
	StorageURL     string
	StorageProject string
	StorageBucket  string
	// Injected product config/secret names (presence only in status endpoints).
	AppSharedSecret string
	ProductMode     string
}

func loadConfig() (config, error) {
	portRaw := strings.TrimSpace(os.Getenv("PORT"))
	if portRaw == "" {
		return config{}, fmt.Errorf("PORT is required")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return config{}, fmt.Errorf("PORT must be an integer 1–65535, got %q", portRaw)
	}

	level := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_LOG_LEVEL")))
	if level == "" {
		level = "info"
	}
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return config{}, fmt.Errorf("FORGE_LOG_LEVEL must be debug|info|warn|error, got %q", level)
	}

	name := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if name == "" {
		name = "incident-api"
	}
	version := strings.TrimSpace(os.Getenv("FORGE_SERVICE_VERSION"))
	if version == "" {
		version = "0.1.0"
	}
	env := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if env == "" {
		env = "development"
	}

	eventsURL := strings.TrimSpace(os.Getenv("FORGE_EVENTS_URL"))
	if eventsURL == "" {
		eventsURL = "http://host.docker.internal:4105"
	}

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if strings.Contains(dbURL, "postgres:5432/forge") || strings.Contains(dbURL, ":5001/forge") {
		return config{}, fmt.Errorf("refusing Control database URL")
	}

	productAuth := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_PRODUCT_AUTH")))
	if productAuth == "" {
		productAuth = strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTH_MODE")))
	}
	if productAuth == "" {
		productAuth = "dev"
	}
	if productAuth == "enforced" {
		productAuth = "enforce"
	}
	switch productAuth {
	case "dev", "enforce":
	default:
		return config{}, fmt.Errorf("FORGE_PRODUCT_AUTH must be dev|enforce, got %q", productAuth)
	}

	identityURL := strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL"))
	if identityURL == "" {
		identityURL = "http://host.docker.internal:4002"
	}
	if productAuth == "enforce" && identityURL == "" {
		return config{}, fmt.Errorf("FORGE_IDENTITY_URL required when product auth is enforce")
	}

	otelEnabled := parseBool(os.Getenv("FORGE_OTEL_ENABLED"), false)
	otelEndpoint := strings.TrimSpace(os.Getenv("FORGE_OTEL_EXPORTER_ENDPOINT"))
	if otelEndpoint == "" {
		otelEndpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if otelEndpoint == "" {
		otelEndpoint = "http://host.docker.internal:4317"
	}

	storageURL := strings.TrimSpace(os.Getenv("FORGE_STORAGE_URL"))
	if storageURL == "" {
		storageURL = "http://host.docker.internal:4107"
	}
	storageProject := strings.TrimSpace(os.Getenv("FORGE_STORAGE_PROJECT"))
	if storageProject == "" {
		storageProject = strings.TrimSpace(os.Getenv("FORGE_PROJECT"))
	}
	storageBucket := strings.TrimSpace(os.Getenv("FORGE_STORAGE_BUCKET"))
	if storageBucket == "" {
		storageBucket = strings.TrimSpace(os.Getenv("STORAGE_BUCKET"))
	}
	if storageBucket == "" {
		storageBucket = "artifacts"
	}

	return config{
		Port:            port,
		ServiceName:     name,
		ServiceVersion:  version,
		LogLevel:        level,
		Env:             env,
		EventsURL:       eventsURL,
		DatabaseURL:     dbURL,
		ProductAuth:     productAuth,
		IdentityURL:     identityURL,
		ProjectID:       strings.TrimSpace(os.Getenv("FORGE_PROJECT")),
		OTELEnabled:     otelEnabled,
		OTELEndpoint:    otelEndpoint,
		StorageURL:      storageURL,
		StorageProject:  storageProject,
		StorageBucket:   storageBucket,
		AppSharedSecret: os.Getenv("APP_SHARED_SECRET"),
		ProductMode:     strings.TrimSpace(os.Getenv("PRODUCT_MODE")),
	}, nil
}

func parseBool(raw string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// sensitiveValues returns plaintext values that must never appear in logs.
func (c config) sensitiveValues() []string {
	out := make([]string, 0, 2)
	if c.DatabaseURL != "" {
		out = append(out, c.DatabaseURL)
	}
	if c.AppSharedSecret != "" {
		out = append(out, c.AppSharedSecret)
	}
	return out
}
