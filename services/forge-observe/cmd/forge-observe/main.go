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

	"forge.local/services/forge-observe/internal/api"
	"forge.local/services/forge-observe/internal/backends"
	"forge.local/services/forge-observe/internal/config"
	"forge.local/services/forge-observe/internal/correlation"
	"forge.local/services/forge-observe/internal/health"
	"forge.local/services/forge-observe/internal/identity"
	"forge.local/services/forge-observe/internal/logs"
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
	log.Info("starting forge-observe",
		"port", cfg.Port,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
		"log_level", cfg.LogLevel,
		"loki_url", cfg.LokiURL,
		"tempo_url", cfg.TempoURL,
		"prometheus_url", cfg.PrometheusURL,
		"backend_timeout_ms", int(cfg.BackendTimeout.Milliseconds()),
		"required_backends", joinBackends(cfg.RequiredBackends),
		"log_query_max_limit", cfg.LogQueryMaxLimit,
		"log_query_max_range_h", int(cfg.LogQueryMaxRange.Hours()),
		"auth_mode", cfg.AuthMode,
		"identity_url", cfg.IdentityURL,
		"shutdown_grace_seconds", int(cfg.ShutdownGrace.Seconds()),
		correlation.AttrService, cfg.ServiceName,
	)

	metrics := &backends.Metrics{}
	opts := backends.Options{
		Timeout: cfg.BackendTimeout,
		Metrics: metrics,
		LogChange: func(name config.BackendName, up bool, err error) {
			attrs := []any{
				"backend", string(name),
				"up", up,
				"span", "observe.backend.health",
				"forge_observe_backend_up", boolToInt(up),
			}
			if err != nil {
				attrs = append(attrs, "error", err.Error())
			}
			if up {
				log.Info("backend connectivity restored", attrs...)
			} else {
				log.Warn("backend connectivity lost", attrs...)
			}
		},
	}

	loki := backends.NewLoki(cfg.LokiURL, opts)
	reg := &backends.Registry{
		Loki:       loki,
		Tempo:      backends.NewTempo(cfg.TempoURL, opts),
		Prometheus: backends.NewPrometheus(cfg.PrometheusURL, opts),
		Required:   cfg.RequiredBackends,
	}

	authMode, err := identity.ParseAuthMode(cfg.AuthMode)
	if err != nil {
		return err
	}
	if authMode == identity.AuthModeEnforce {
		log.Info("FORGE_AUTH_MODE=enforce — log queries require Identity tokens + project.read")
	} else {
		log.Warn("FORGE_AUTH_MODE=dev — log query auth bypassed (insecure)")
	}
	var idClient identity.Client
	if authMode == identity.AuthModeEnforce || cfg.IdentityURL != "" {
		idClient = identity.NewHTTPClient(cfg.IdentityURL, cfg.AuthzCacheTTLS, log)
	}
	authGate := &identity.Gate{Mode: authMode, Client: idClient, Log: log, Action: "project.read"}

	logCaps := logs.Caps{
		MaxLimit:     cfg.LogQueryMaxLimit,
		MaxRange:     cfg.LogQueryMaxRange,
		DefaultSince: time.Hour,
	}
	logMetrics := &logs.Metrics{}
	logSvc := &logs.Service{Loki: loki, Caps: logCaps, Log: log, Metrics: logMetrics}

	mux := http.NewServeMux()
	health.NewHandler(reg, reg, cfg.ServiceName, cfg.ServiceVersion).Register(mux)
	(&api.LogsHandler{Service: logSvc, Caps: logCaps, Auth: authGate, Log: log}).Register(mux)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

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
		return err
	case sig := <-sigCh:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("shutdown complete",
		"forge_observe_backend_up_loki", metrics.LokiUp.Load(),
		"forge_observe_backend_up_tempo", metrics.TempoUp.Load(),
		"forge_observe_backend_up_prometheus", metrics.PrometheusUp.Load(),
	)
	return nil
}

func joinBackends(names []config.BackendName) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = string(n)
	}
	return strings.Join(parts, ",")
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
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
