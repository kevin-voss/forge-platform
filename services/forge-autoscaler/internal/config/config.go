package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds env-based runtime settings for forge-autoscaler.
type Config struct {
	Port           int
	ServiceName    string
	ServiceVersion string
	LogLevel       string
	Env            string
	AuthMode       string
	ShutdownGrace  time.Duration

	DatabaseURL            string
	DatabasePoolMax        int
	DatabaseMigrateOnStart bool

	EvalInterval time.Duration

	ObserveURL       string
	GatewayAdminURL  string
	EventsURL        string
	RuntimeURL       string
	ControlURL       string
	MetricSourceMode string // auto|fake — fake forces FakeSource for demos/tests

	NodeScaleUpCooldown    time.Duration
	NodeScaleDownCooldown  time.Duration
	ReservationThreshold   float64
	NodeScaleDefaultMax    int
	NodeScaleEnabled       bool
	NodeScaleDownEnabled   bool
	UnderutilThreshold     float64
	UnderutilWindow        time.Duration
	MaxDeletesPerWindow    int
	ScaleDownRetryUncordon bool
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	portRaw := firstNonEmpty(os.Getenv("FORGE_AUTOSCALER_PORT"), os.Getenv("PORT"))
	if portRaw == "" {
		portRaw = "4112"
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("PORT/FORGE_AUTOSCALER_PORT must be an integer 1–65535, got %q", portRaw)
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
		name = "forge-autoscaler"
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

	dbURL := firstNonEmpty(os.Getenv("FORGE_AUTOSCALER_DB_URL"), os.Getenv("FORGE_DATABASE_URL"))
	if dbURL == "" {
		dbURL = "postgres://forge:forge@127.0.0.1:5001/forge_autoscaler?sslmode=disable"
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
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_DATABASE_MIGRATE_ON_START"))) {
	case "false", "0", "no":
		migrateOnStart = false
	}

	evalRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_EVAL_INTERVAL_MS"))
	if evalRaw == "" {
		evalRaw = "15000"
	}
	evalMs, err := strconv.Atoi(evalRaw)
	if err != nil || evalMs < 1 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_EVAL_INTERVAL_MS must be a positive integer, got %q", evalRaw)
	}

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_METRIC_SOURCE")))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "fake":
	default:
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_METRIC_SOURCE must be auto|fake, got %q", mode)
	}

	controlURL := firstNonEmpty(os.Getenv("FORGE_CONTROL_URL"), os.Getenv("FORGE_AUTOSCALER_CONTROL_URL"))
	if controlURL == "" {
		controlURL = "http://127.0.0.1:4001"
	}

	cooldownRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS"))
	if cooldownRaw == "" {
		cooldownRaw = "60"
	}
	cooldownSecs, err := strconv.Atoi(cooldownRaw)
	if err != nil || cooldownSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_NODE_SCALE_UP_COOLDOWN_SECONDS must be a non-negative integer, got %q", cooldownRaw)
	}

	threshRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_RESERVATION_THRESHOLD"))
	if threshRaw == "" {
		threshRaw = "0.85"
	}
	thresh, err := strconv.ParseFloat(threshRaw, 64)
	if err != nil || thresh <= 0 || thresh > 1 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_RESERVATION_THRESHOLD must be in (0,1], got %q", threshRaw)
	}

	maxNodesRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_DEFAULT_MAX_NODES"))
	if maxNodesRaw == "" {
		maxNodesRaw = "100"
	}
	maxNodes, err := strconv.Atoi(maxNodesRaw)
	if err != nil || maxNodes < 1 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_NODE_DEFAULT_MAX_NODES must be a positive integer, got %q", maxNodesRaw)
	}

	nodeScaleEnabled := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_SCALE_UP_ENABLED"))) {
	case "false", "0", "no":
		nodeScaleEnabled = false
	}

	nodeScaleDownEnabled := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_SCALE_DOWN_ENABLED"))) {
	case "false", "0", "no":
		nodeScaleDownEnabled = false
	}

	sdCooldownRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS"))
	if sdCooldownRaw == "" {
		sdCooldownRaw = "300"
	}
	sdCooldownSecs, err := strconv.Atoi(sdCooldownRaw)
	if err != nil || sdCooldownSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_NODE_SCALE_DOWN_COOLDOWN_SECONDS must be a non-negative integer, got %q", sdCooldownRaw)
	}

	utilRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_THRESHOLD"))
	if utilRaw == "" {
		utilRaw = "0.25"
	}
	utilThresh, err := strconv.ParseFloat(utilRaw, 64)
	if err != nil || utilThresh < 0 || utilThresh > 1 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_THRESHOLD must be in [0,1], got %q", utilRaw)
	}

	windowRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS"))
	if windowRaw == "" {
		windowRaw = "300"
	}
	windowSecs, err := strconv.Atoi(windowRaw)
	if err != nil || windowSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_NODE_UNDERUTILIZATION_WINDOW_SECONDS must be a non-negative integer, got %q", windowRaw)
	}

	maxDelRaw := strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_MAX_DELETES_PER_WINDOW"))
	if maxDelRaw == "" {
		maxDelRaw = "1"
	}
	maxDeletes, err := strconv.Atoi(maxDelRaw)
	if err != nil || maxDeletes < 1 {
		return Config{}, fmt.Errorf("FORGE_AUTOSCALER_NODE_MAX_DELETES_PER_WINDOW must be a positive integer, got %q", maxDelRaw)
	}

	retryUncordon := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_AUTOSCALER_NODE_SCALE_DOWN_UNCORDON_ON_BLOCK"))) {
	case "false", "0", "no":
		retryUncordon = false
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
		DatabasePoolMax:        poolMax,
		DatabaseMigrateOnStart: migrateOnStart,
		EvalInterval:           time.Duration(evalMs) * time.Millisecond,
		ObserveURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("FORGE_OBSERVE_URL")), "/"),
		GatewayAdminURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("FORGE_GATEWAY_ADMIN_URL")), "/"),
		EventsURL:              strings.TrimRight(strings.TrimSpace(os.Getenv("FORGE_EVENTS_URL")), "/"),
		RuntimeURL:             strings.TrimRight(strings.TrimSpace(os.Getenv("FORGE_RUNTIME_URL")), "/"),
		ControlURL:             strings.TrimRight(strings.TrimSpace(controlURL), "/"),
		MetricSourceMode:       mode,
		NodeScaleUpCooldown:    time.Duration(cooldownSecs) * time.Second,
		NodeScaleDownCooldown:  time.Duration(sdCooldownSecs) * time.Second,
		ReservationThreshold:   thresh,
		NodeScaleDefaultMax:    maxNodes,
		NodeScaleEnabled:       nodeScaleEnabled,
		NodeScaleDownEnabled:   nodeScaleDownEnabled,
		UnderutilThreshold:     utilThresh,
		UnderutilWindow:        time.Duration(windowSecs) * time.Second,
		MaxDeletesPerWindow:    maxDeletes,
		ScaleDownRetryUncordon: retryUncordon,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
