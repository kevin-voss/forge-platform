package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"forge.local/services/forge-gateway/internal/admin"
	"forge.local/services/forge-gateway/internal/config"
	"forge.local/services/forge-gateway/internal/health"
	"forge.local/services/forge-gateway/internal/middleware"
	"forge.local/services/forge-gateway/internal/proxy"
	"forge.local/services/forge-gateway/internal/routes"
	gwync "forge.local/services/forge-gateway/internal/sync"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.ServiceName, cfg.LogLevel)
	log.Info("starting forge-gateway",
		"port", cfg.Port,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
		"auth_mode", cfg.AuthMode,
		"log_level", cfg.LogLevel,
		"shutdown_grace_seconds", int(cfg.ShutdownGrace.Seconds()),
		"static_routes", cfg.StaticRoutesPath,
		"route_source", cfg.RouteSource,
		"route_sync_interval_seconds", int(cfg.RouteSyncInterval.Seconds()),
		"host_pattern", cfg.HostPattern,
		"sync_enabled", cfg.SyncEnabled,
		"upstream_probe_interval_seconds", int(cfg.UpstreamProbeInterval.Seconds()),
		"upstream_probe_path", cfg.UpstreamProbePath,
		"upstream_failure_threshold", cfg.UpstreamFailureThreshold,
		"upstream_success_threshold", cfg.UpstreamSuccessThreshold,
		"upstream_trust_runtime_status", cfg.UpstreamTrustRuntime,
		"request_id_header", cfg.RequestIDHeader,
		"proxy_connect_timeout_seconds", int(cfg.ProxyConnectTimeout.Seconds()),
		"proxy_response_header_timeout_seconds", int(cfg.ProxyResponseHeaderTimeout.Seconds()),
		"proxy_overall_timeout_seconds", int(cfg.ProxyOverallTimeout.Seconds()),
		"trust_inbound_xff", cfg.TrustInboundXFF,
	)

	table := routes.NewTable()
	if cfg.StaticRoutesPath != "" {
		if err := table.LoadFile(cfg.StaticRoutesPath); err != nil {
			return err
		}
		log.Info("loaded static routes", "path", cfg.StaticRoutesPath, "route_count", table.Len())
	}

	ready := health.NewReadiness()
	tracker := health.NewUpstreamTracker(health.UpstreamConfig{
		ProbeInterval:      cfg.UpstreamProbeInterval,
		ProbePath:          cfg.UpstreamProbePath,
		FailureThreshold:   cfg.UpstreamFailureThreshold,
		SuccessThreshold:   cfg.UpstreamSuccessThreshold,
		TrustRuntimeStatus: cfg.UpstreamTrustRuntime,
	}, log)
	if table.Len() > 0 {
		urls := make([]string, 0)
		for _, r := range table.Snapshot() {
			for _, u := range r.Upstreams {
				urls = append(urls, u.URL)
			}
		}
		tracker.Reconcile(urls)
	}

	proxyHandler := proxy.NewHandler(table, log, tracker, proxy.Config{
		RequestIDHeader:       cfg.RequestIDHeader,
		ConnectTimeout:        cfg.ProxyConnectTimeout,
		ResponseHeaderTimeout: cfg.ProxyResponseHeaderTimeout,
		OverallTimeout:        cfg.ProxyOverallTimeout,
		TrustInboundXFF:       cfg.TrustInboundXFF,
	})

	var syncer *gwync.Syncer
	if cfg.SyncEnabled {
		source, err := gwync.BuildSource(cfg.RouteSource, cfg.ControlURL, cfg.RuntimeURL, cfg.UpstreamHost, nil)
		if err != nil {
			return err
		}
		syncer = gwync.New(gwync.Config{
			Table:    table,
			Proxy:    proxyHandler,
			Tracker:  tracker,
			Source:   source,
			Pattern:  cfg.HostPattern,
			Interval: cfg.RouteSyncInterval,
			Log:      log,
		})
	} else {
		log.Info("route sync disabled (set FORGE_CONTROL_URL / FORGE_RUNTIME_URL to enable)")
		// Still expose refresh so callers get a clear response.
		syncer = gwync.New(gwync.Config{
			Table:    table,
			Proxy:    proxyHandler,
			Tracker:  tracker,
			Source:   nil,
			Pattern:  cfg.HostPattern,
			Interval: 0,
			Log:      log,
		})
	}

	mux := http.NewServeMux()
	health.NewHandler(ready).Register(mux)
	admin.NewRoutesHandler(table, proxyHandler, log).Register(mux)
	syncer.Register(mux)
	mux.Handle("/", proxyHandler)

	var handler http.Handler = mux
	handler = middleware.RequestID(cfg.RequestIDHeader)(handler)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ready.MarkReady()

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	if cfg.SyncEnabled {
		go syncer.Run(bgCtx)
	}
	go tracker.RunProber(bgCtx, table)

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-errCh:
		bgCancel()
		return err
	case sig := <-sigCh:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	bgCancel()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}

func newLogger(serviceName, level string) *slog.Logger {
	var min slog.Level
	switch strings.ToLower(level) {
	case "debug":
		min = slog.LevelDebug
	case "warn":
		min = slog.LevelWarn
	case "error":
		min = slog.LevelError
	default:
		min = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       min,
		ReplaceAttr: replaceLogAttr,
	})
	return slog.New(handler).With("service", serviceName)
}

func replaceLogAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		return slog.String("timestamp", a.Value.Time().UTC().Format(time.RFC3339))
	case slog.MessageKey:
		return slog.String("message", a.Value.String())
	case slog.LevelKey:
		level, ok := a.Value.Any().(slog.Level)
		if !ok {
			return slog.String("level", "info")
		}
		switch {
		case level < slog.LevelInfo:
			return slog.String("level", "debug")
		case level < slog.LevelWarn:
			return slog.String("level", "info")
		case level < slog.LevelError:
			return slog.String("level", "warn")
		default:
			return slog.String("level", "error")
		}
	default:
		return a
	}
}
