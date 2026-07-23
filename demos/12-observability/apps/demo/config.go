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
	OTELEnabled    bool
	OTELEndpoint   string
}

func loadConfig() (config, error) {
	port := 8080
	if raw := strings.TrimSpace(os.Getenv("PORT")); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			return config{}, fmt.Errorf("invalid PORT %q", raw)
		}
		port = p
	}
	service := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if service == "" {
		service = "demo-app"
	}
	version := strings.TrimSpace(os.Getenv("FORGE_SERVICE_VERSION"))
	if version == "" {
		version = "0.1.0"
	}
	level := strings.TrimSpace(os.Getenv("FORGE_LOG_LEVEL"))
	if level == "" {
		level = "info"
	}
	env := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if env == "" {
		env = "development"
	}
	enabled := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_OTEL_ENABLED"))) {
	case "false", "0", "no":
		enabled = false
	}
	endpoint := strings.TrimSpace(os.Getenv("FORGE_OTEL_EXPORTER_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		// Workloads publish host ports on the Docker bridge; reach the
		// collector via the host gateway (Docker Desktop / Compose).
		endpoint = "http://host.docker.internal:4317"
	}
	return config{
		Port:           port,
		ServiceName:    service,
		ServiceVersion: version,
		LogLevel:       level,
		Env:            env,
		OTELEnabled:    enabled,
		OTELEndpoint:   endpoint,
	}, nil
}
