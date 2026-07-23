package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type config struct {
	Port            int
	ServiceName     string
	ServiceVersion  string
	LogLevel        string
	Env             string
	EventsURL       string
	IdempotencyKey  string
	DefaultCount    int
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
		name = "demo-events-producer"
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
		eventsURL = "http://forge-events:8080"
	}
	idemKey := strings.TrimSpace(os.Getenv("FORGE_DEMO_IDEMPOTENCY_KEY"))
	if idemKey == "" {
		idemKey = "demo-11-idempotency-key"
	}
	count := 5
	if raw := strings.TrimSpace(os.Getenv("FORGE_DEMO_EVENT_COUNT")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			return config{}, fmt.Errorf("FORGE_DEMO_EVENT_COUNT must be 1–50, got %q", raw)
		}
		count = n
	}

	return config{
		Port:           port,
		ServiceName:    name,
		ServiceVersion: version,
		LogLevel:       level,
		Env:            env,
		EventsURL:      strings.TrimRight(eventsURL, "/"),
		IdempotencyKey: idemKey,
		DefaultCount:   count,
	}, nil
}
