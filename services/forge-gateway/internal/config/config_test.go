package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_SERVICE_NAME", "")
	t.Setenv("FORGE_SERVICE_VERSION", "")
	t.Setenv("FORGE_LOG_LEVEL", "")
	t.Setenv("FORGE_ENV", "")
	t.Setenv("FORGE_AUTH_MODE", "")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "")
	t.Setenv("FORGE_GATEWAY_STATIC_ROUTES", "")
	t.Setenv("FORGE_CONTROL_URL", "")
	t.Setenv("FORGE_RUNTIME_URL", "")
	t.Setenv("FORGE_ROUTE_SOURCE", "")
	t.Setenv("FORGE_ROUTE_SYNC_INTERVAL_SECONDS", "")
	t.Setenv("FORGE_HOST_PATTERN", "")
	t.Setenv("FORGE_UPSTREAM_HOST", "")
	t.Setenv("FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS", "")
	t.Setenv("FORGE_UPSTREAM_PROBE_PATH", "")
	t.Setenv("FORGE_UPSTREAM_FAILURE_THRESHOLD", "")
	t.Setenv("FORGE_UPSTREAM_SUCCESS_THRESHOLD", "")
	t.Setenv("FORGE_UPSTREAM_TRUST_RUNTIME_STATUS", "")
	t.Setenv("FORGE_REQUEST_ID_HEADER", "")
	t.Setenv("FORGE_PROXY_CONNECT_TIMEOUT_SECONDS", "")
	t.Setenv("FORGE_PROXY_RESPONSE_HEADER_TIMEOUT_SECONDS", "")
	t.Setenv("FORGE_PROXY_OVERALL_TIMEOUT_SECONDS", "")
	t.Setenv("FORGE_TRUST_INBOUND_XFF", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.ServiceName != "forge-gateway" {
		t.Fatalf("ServiceName = %q, want forge-gateway", cfg.ServiceName)
	}
	if cfg.ServiceVersion != "0.1.0" {
		t.Fatalf("ServiceVersion = %q, want 0.1.0", cfg.ServiceVersion)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.Env != "development" {
		t.Fatalf("Env = %q, want development", cfg.Env)
	}
	if cfg.AuthMode != "dev" {
		t.Fatalf("AuthMode = %q, want dev", cfg.AuthMode)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 10s", cfg.ShutdownGrace)
	}
	if cfg.StaticRoutesPath != "" {
		t.Fatalf("StaticRoutesPath = %q, want empty", cfg.StaticRoutesPath)
	}
	if cfg.RouteSource != "control" {
		t.Fatalf("RouteSource = %q, want control", cfg.RouteSource)
	}
	if cfg.RouteSyncInterval != 10*time.Second {
		t.Fatalf("RouteSyncInterval = %v, want 10s", cfg.RouteSyncInterval)
	}
	if cfg.HostPattern != "{service}.{project}.demo.localhost" {
		t.Fatalf("HostPattern = %q", cfg.HostPattern)
	}
	if cfg.UpstreamHost != "127.0.0.1" {
		t.Fatalf("UpstreamHost = %q", cfg.UpstreamHost)
	}
	if cfg.SyncEnabled {
		t.Fatal("SyncEnabled should be false without platform URLs")
	}
	if cfg.UpstreamProbeInterval != 5*time.Second {
		t.Fatalf("UpstreamProbeInterval = %v, want 5s", cfg.UpstreamProbeInterval)
	}
	if cfg.UpstreamProbePath != "/health/ready" {
		t.Fatalf("UpstreamProbePath = %q", cfg.UpstreamProbePath)
	}
	if cfg.UpstreamFailureThreshold != 3 || cfg.UpstreamSuccessThreshold != 2 {
		t.Fatalf("thresholds fail=%d success=%d", cfg.UpstreamFailureThreshold, cfg.UpstreamSuccessThreshold)
	}
	if !cfg.UpstreamTrustRuntime {
		t.Fatal("UpstreamTrustRuntime should default true")
	}
	if cfg.RequestIDHeader != "X-Request-Id" {
		t.Fatalf("RequestIDHeader = %q", cfg.RequestIDHeader)
	}
	if cfg.ProxyConnectTimeout != 5*time.Second || cfg.ProxyResponseHeaderTimeout != 15*time.Second || cfg.ProxyOverallTimeout != 30*time.Second {
		t.Fatalf("proxy timeouts: connect=%v resp=%v overall=%v", cfg.ProxyConnectTimeout, cfg.ProxyResponseHeaderTimeout, cfg.ProxyOverallTimeout)
	}
	if cfg.TrustInboundXFF {
		t.Fatal("TrustInboundXFF should default false")
	}
}

func TestLoadInvalidPort(t *testing.T) {
	cases := []string{"", "abc", "0", "-1", "70000"}
	for _, port := range cases {
		t.Run("PORT="+port, func(t *testing.T) {
			t.Setenv("PORT", port)
			if _, err := Load(); err == nil {
				t.Fatal("expected error for invalid PORT")
			}
		})
	}
}

func TestLoadCustomValues(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("FORGE_SERVICE_NAME", "gw")
	t.Setenv("FORGE_SERVICE_VERSION", "1.2.3")
	t.Setenv("FORGE_LOG_LEVEL", "DEBUG")
	t.Setenv("FORGE_ENV", "test")
	t.Setenv("FORGE_AUTH_MODE", "dev")
	t.Setenv("FORGE_SHUTDOWN_GRACE_SECONDS", "5")
	t.Setenv("FORGE_GATEWAY_STATIC_ROUTES", "/etc/forge/routes.json")
	t.Setenv("FORGE_CONTROL_URL", "http://forge-control:8080")
	t.Setenv("FORGE_RUNTIME_URL", "http://forge-runtime:8080")
	t.Setenv("FORGE_ROUTE_SOURCE", "runtime")
	t.Setenv("FORGE_ROUTE_SYNC_INTERVAL_SECONDS", "3")
	t.Setenv("FORGE_HOST_PATTERN", "{service}.{project}.local")
	t.Setenv("FORGE_UPSTREAM_HOST", "host.docker.internal")
	t.Setenv("FORGE_UPSTREAM_PROBE_INTERVAL_SECONDS", "2")
	t.Setenv("FORGE_UPSTREAM_PROBE_PATH", "/readyz")
	t.Setenv("FORGE_UPSTREAM_FAILURE_THRESHOLD", "5")
	t.Setenv("FORGE_UPSTREAM_SUCCESS_THRESHOLD", "4")
	t.Setenv("FORGE_UPSTREAM_TRUST_RUNTIME_STATUS", "false")
	t.Setenv("FORGE_REQUEST_ID_HEADER", "X-Correlation-Id")
	t.Setenv("FORGE_PROXY_CONNECT_TIMEOUT_SECONDS", "2")
	t.Setenv("FORGE_PROXY_RESPONSE_HEADER_TIMEOUT_SECONDS", "8")
	t.Setenv("FORGE_PROXY_OVERALL_TIMEOUT_SECONDS", "12")
	t.Setenv("FORGE_TRUST_INBOUND_XFF", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9090 || cfg.ServiceName != "gw" || cfg.ServiceVersion != "1.2.3" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.ShutdownGrace != 5*time.Second {
		t.Fatalf("ShutdownGrace = %v, want 5s", cfg.ShutdownGrace)
	}
	if cfg.StaticRoutesPath != "/etc/forge/routes.json" {
		t.Fatalf("StaticRoutesPath = %q", cfg.StaticRoutesPath)
	}
	if !cfg.SyncEnabled {
		t.Fatal("SyncEnabled should be true with control+runtime URLs")
	}
	if cfg.RouteSource != "runtime" || cfg.RouteSyncInterval != 3*time.Second {
		t.Fatalf("sync cfg: %+v", cfg)
	}
	if cfg.UpstreamHost != "host.docker.internal" {
		t.Fatalf("UpstreamHost = %q", cfg.UpstreamHost)
	}
	if cfg.UpstreamProbeInterval != 2*time.Second || cfg.UpstreamProbePath != "/readyz" {
		t.Fatalf("probe cfg: %+v", cfg)
	}
	if cfg.UpstreamFailureThreshold != 5 || cfg.UpstreamSuccessThreshold != 4 {
		t.Fatalf("thresholds: %+v", cfg)
	}
	if cfg.UpstreamTrustRuntime {
		t.Fatal("UpstreamTrustRuntime should be false")
	}
	if cfg.RequestIDHeader != "X-Correlation-Id" {
		t.Fatalf("RequestIDHeader = %q", cfg.RequestIDHeader)
	}
	if cfg.ProxyConnectTimeout != 2*time.Second || cfg.ProxyResponseHeaderTimeout != 8*time.Second || cfg.ProxyOverallTimeout != 12*time.Second {
		t.Fatalf("proxy timeouts: %+v", cfg)
	}
	if !cfg.TrustInboundXFF {
		t.Fatal("TrustInboundXFF should be true")
	}
}

func TestLoadInvalidRouteSource(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_ROUTE_SOURCE", "kafka")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid FORGE_ROUTE_SOURCE")
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_LOG_LEVEL", "verbose")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid FORGE_LOG_LEVEL")
	}
}
