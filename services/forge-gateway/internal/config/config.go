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
	DiscoveryURL      string
	RouteSource       string
	RouteSyncInterval time.Duration
	HostPattern       string
	UpstreamHost      string
	SyncEnabled       bool

	UpstreamProbeInterval    time.Duration
	UpstreamProbePath        string
	UpstreamFailureThreshold int
	UpstreamSuccessThreshold int
	UpstreamTrustRuntime     bool

	RequestIDHeader            string
	ProxyConnectTimeout        time.Duration
	ProxyResponseHeaderTimeout time.Duration
	ProxyOverallTimeout        time.Duration
	TrustInboundXFF            bool

	WSEnabled         bool
	SSEEnabled        bool
	WSIdleTimeout     time.Duration
	StreamReadTimeout time.Duration
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
	discoveryURL := strings.TrimSpace(os.Getenv("FORGE_DISCOVERY_URL"))

	routeSource := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_ROUTE_SOURCE")))
	if routeSource == "" {
		routeSource = "control"
	}
	switch routeSource {
	case "control", "runtime", "discovery":
	default:
		return Config{}, fmt.Errorf("FORGE_ROUTE_SOURCE must be control|runtime|discovery, got %q", routeSource)
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

	// Sync is enabled when the configured source's required URLs are present.
	// control → Control; runtime → Control+Runtime; discovery → Discovery.
	syncEnabled := false
	switch routeSource {
	case "control":
		syncEnabled = controlURL != ""
	case "runtime":
		syncEnabled = controlURL != "" && runtimeURL != ""
	case "discovery":
		syncEnabled = discoveryURL != ""
	}

	probeIntervalRaw := strings.TrimSpace(os.Getenv("FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS"))
	if probeIntervalRaw == "" {
		probeIntervalRaw = "5"
	}
	probeIntervalSecs, err := strconv.Atoi(probeIntervalRaw)
	if err != nil || probeIntervalSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS must be a non-negative integer, got %q", probeIntervalRaw)
	}

	probePath := strings.TrimSpace(os.Getenv("FORGE_UPSTREAM_PROBE_PATH"))
	if probePath == "" {
		probePath = "/health/ready"
	}
	if !strings.HasPrefix(probePath, "/") {
		return Config{}, fmt.Errorf("FORGE_UPSTREAM_PROBE_PATH must start with /, got %q", probePath)
	}

	failRaw := strings.TrimSpace(os.Getenv("FORGE_UPSTREAM_FAILURE_THRESHOLD"))
	if failRaw == "" {
		failRaw = "3"
	}
	failThreshold, err := strconv.Atoi(failRaw)
	if err != nil || failThreshold < 1 {
		return Config{}, fmt.Errorf("FORGE_UPSTREAM_FAILURE_THRESHOLD must be a positive integer, got %q", failRaw)
	}

	successRaw := strings.TrimSpace(os.Getenv("FORGE_UPSTREAM_SUCCESS_THRESHOLD"))
	if successRaw == "" {
		successRaw = "2"
	}
	successThreshold, err := strconv.Atoi(successRaw)
	if err != nil || successThreshold < 1 {
		return Config{}, fmt.Errorf("FORGE_UPSTREAM_SUCCESS_THRESHOLD must be a positive integer, got %q", successRaw)
	}

	trustRaw := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_UPSTREAM_TRUST_RUNTIME_STATUS")))
	if trustRaw == "" {
		trustRaw = "true"
	}
	var trustRuntime bool
	switch trustRaw {
	case "true", "1", "yes":
		trustRuntime = true
	case "false", "0", "no":
		trustRuntime = false
	default:
		return Config{}, fmt.Errorf("FORGE_UPSTREAM_TRUST_RUNTIME_STATUS must be true|false, got %q", trustRaw)
	}

	requestIDHeader := strings.TrimSpace(os.Getenv("FORGE_REQUEST_ID_HEADER"))
	if requestIDHeader == "" {
		requestIDHeader = "X-Request-Id"
	}

	connectRaw := strings.TrimSpace(os.Getenv("FORGE_PROXY_CONNECT_TIMEOUT_SECONDS"))
	if connectRaw == "" {
		connectRaw = "5"
	}
	connectSecs, err := strconv.Atoi(connectRaw)
	if err != nil || connectSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_PROXY_CONNECT_TIMEOUT_SECONDS must be a non-negative integer, got %q", connectRaw)
	}

	respHdrRaw := strings.TrimSpace(os.Getenv("FORGE_PROXY_RESPONSE_HEADER_TIMEOUT_SECONDS"))
	if respHdrRaw == "" {
		respHdrRaw = "15"
	}
	respHdrSecs, err := strconv.Atoi(respHdrRaw)
	if err != nil || respHdrSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_PROXY_RESPONSE_HEADER_TIMEOUT_SECONDS must be a non-negative integer, got %q", respHdrRaw)
	}

	overallRaw := strings.TrimSpace(os.Getenv("FORGE_PROXY_OVERALL_TIMEOUT_SECONDS"))
	if overallRaw == "" {
		overallRaw = "30"
	}
	overallSecs, err := strconv.Atoi(overallRaw)
	if err != nil || overallSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_PROXY_OVERALL_TIMEOUT_SECONDS must be a non-negative integer, got %q", overallRaw)
	}

	xffTrustRaw := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_TRUST_INBOUND_XFF")))
	if xffTrustRaw == "" {
		xffTrustRaw = "false"
	}
	var trustInboundXFF bool
	switch xffTrustRaw {
	case "true", "1", "yes":
		trustInboundXFF = true
	case "false", "0", "no":
		trustInboundXFF = false
	default:
		return Config{}, fmt.Errorf("FORGE_TRUST_INBOUND_XFF must be true|false, got %q", xffTrustRaw)
	}

	wsEnabled, err := parseBoolEnv("FORGE_WS_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	sseEnabled, err := parseBoolEnv("FORGE_SSE_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	wsIdleRaw := strings.TrimSpace(os.Getenv("FORGE_WS_IDLE_TIMEOUT_SECONDS"))
	if wsIdleRaw == "" {
		wsIdleRaw = "300"
	}
	wsIdleSecs, err := strconv.Atoi(wsIdleRaw)
	if err != nil || wsIdleSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_WS_IDLE_TIMEOUT_SECONDS must be a non-negative integer, got %q", wsIdleRaw)
	}

	streamReadRaw := strings.TrimSpace(os.Getenv("FORGE_STREAM_READ_TIMEOUT_SECONDS"))
	if streamReadRaw == "" {
		streamReadRaw = "0"
	}
	streamReadSecs, err := strconv.Atoi(streamReadRaw)
	if err != nil || streamReadSecs < 0 {
		return Config{}, fmt.Errorf("FORGE_STREAM_READ_TIMEOUT_SECONDS must be a non-negative integer, got %q", streamReadRaw)
	}

	return Config{
		Port:                       port,
		ServiceName:                name,
		ServiceVersion:             version,
		LogLevel:                   level,
		Env:                        env,
		AuthMode:                   authMode,
		ShutdownGrace:              time.Duration(graceSecs) * time.Second,
		StaticRoutesPath:           strings.TrimSpace(os.Getenv("FORGE_GATEWAY_STATIC_ROUTES")),
		ControlURL:                 controlURL,
		RuntimeURL:                 runtimeURL,
		DiscoveryURL:               discoveryURL,
		RouteSource:                routeSource,
		RouteSyncInterval:          time.Duration(syncSecs) * time.Second,
		HostPattern:                hostPattern,
		UpstreamHost:               upstreamHost,
		SyncEnabled:                syncEnabled,
		UpstreamProbeInterval:      time.Duration(probeIntervalSecs) * time.Second,
		UpstreamProbePath:          probePath,
		UpstreamFailureThreshold:   failThreshold,
		UpstreamSuccessThreshold:   successThreshold,
		UpstreamTrustRuntime:       trustRuntime,
		RequestIDHeader:            requestIDHeader,
		ProxyConnectTimeout:        time.Duration(connectSecs) * time.Second,
		ProxyResponseHeaderTimeout: time.Duration(respHdrSecs) * time.Second,
		ProxyOverallTimeout:        time.Duration(overallSecs) * time.Second,
		TrustInboundXFF:            trustInboundXFF,
		WSEnabled:                  wsEnabled,
		SSEEnabled:                 sseEnabled,
		WSIdleTimeout:              time.Duration(wsIdleSecs) * time.Second,
		StreamReadTimeout:          time.Duration(streamReadSecs) * time.Second,
	}, nil
}

func parseBoolEnv(name string, defaultVal bool) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return defaultVal, nil
	}
	switch raw {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true|false, got %q", name, raw)
	}
}
