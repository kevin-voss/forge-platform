package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds env-based runtime settings for forge-discovery.
type Config struct {
	Port           int
	ServiceName    string
	ServiceVersion string
	LogLevel       string
	Env            string
	AuthMode       string
	ShutdownGrace  time.Duration

	DatabaseURL            string
	DatabaseSchema         string
	DatabasePoolMax        int
	DatabaseMigrateOnStart bool

	ControlURL string

	LeaseSecondsDefault int
	SweepInterval       time.Duration
	ReapAfter           time.Duration
	NodeWatchResync     time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	portRaw := strings.TrimSpace(os.Getenv("PORT"))
	if portRaw == "" {
		portRaw = "8080"
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
		name = "forge-discovery"
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

	dbURL := strings.TrimSpace(os.Getenv("FORGE_DATABASE_URL"))
	if dbURL == "" {
		dbURL = "postgres://forge:forge@localhost:5432/forge?sslmode=disable"
	}
	schema := strings.TrimSpace(os.Getenv("FORGE_DATABASE_SCHEMA"))
	if schema == "" {
		schema = "discovery"
	}
	poolRaw := strings.TrimSpace(os.Getenv("FORGE_DATABASE_POOL_MAX"))
	if poolRaw == "" {
		poolRaw = "10"
	}
	poolMax, err := strconv.Atoi(poolRaw)
	if err != nil || poolMax < 1 {
		return Config{}, fmt.Errorf("FORGE_DATABASE_POOL_MAX must be a positive integer, got %q", poolRaw)
	}
	migrateOnStart := true
	migrateRaw := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_DATABASE_MIGRATE_ON_START")))
	switch migrateRaw {
	case "false", "0", "no":
		migrateOnStart = false
	}

	controlURL := strings.TrimSpace(os.Getenv("FORGE_CONTROL_URL"))
	if controlURL == "" {
		controlURL = "http://forge-control:8080"
	}

	leaseDefault, err := positiveIntEnv("FORGE_DISCOVERY_LEASE_SECONDS_DEFAULT", 20)
	if err != nil {
		return Config{}, err
	}
	sweepSecs, err := positiveIntEnv("FORGE_DISCOVERY_SWEEP_INTERVAL_SECONDS", 5)
	if err != nil {
		return Config{}, err
	}
	reapSecs, err := positiveIntEnv("FORGE_DISCOVERY_REAP_AFTER_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	resyncSecs, err := positiveIntEnv("FORGE_DISCOVERY_NODE_WATCH_RESYNC_SECONDS", 30)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Port:                   port,
		ServiceName:            name,
		ServiceVersion:         version,
		LogLevel:               level,
		Env:                    env,
		AuthMode:               authMode,
		ShutdownGrace:          time.Duration(graceSecs) * time.Second,
		DatabaseURL:            dbURL,
		DatabaseSchema:         schema,
		DatabasePoolMax:        poolMax,
		DatabaseMigrateOnStart: migrateOnStart,
		ControlURL:             strings.TrimRight(controlURL, "/"),
		LeaseSecondsDefault:    leaseDefault,
		SweepInterval:          time.Duration(sweepSecs) * time.Second,
		ReapAfter:              time.Duration(reapSecs) * time.Second,
		NodeWatchResync:        time.Duration(resyncSecs) * time.Second,
	}, nil
}

func positiveIntEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return n, nil
}
