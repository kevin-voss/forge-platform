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

	return config{
		Port:           port,
		ServiceName:    name,
		ServiceVersion: version,
		LogLevel:       level,
		Env:            env,
	}, nil
}
