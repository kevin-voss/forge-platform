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

	"forge.local/services/forge-autoscaler/internal/config"
	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/health"
	httpserver "forge.local/services/forge-autoscaler/internal/http"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
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
	log.Info("starting forge-autoscaler",
		"port", cfg.Port,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
		"auth_mode", cfg.AuthMode,
		"eval_interval_ms", int(cfg.EvalInterval.Milliseconds()),
		"metric_source", cfg.MetricSourceMode,
		"shutdown_grace_seconds", int(cfg.ShutdownGrace.Seconds()),
	)

	ctx := context.Background()
	db, err := policy.Open(ctx, cfg.DatabaseURL, cfg.DatabasePoolMax, cfg.DatabaseMigrateOnStart)
	if err != nil {
		return err
	}
	defer db.Close()

	hub := policy.NewHub(db.Pool, 1000)
	store := &policy.Store{Pool: db.Pool, Hub: hub}

	fake := metrics.NewFakeSource()
	router := &metrics.Router{
		Observe: &metrics.ObserveSource{BaseURL: cfg.ObserveURL},
		Gateway: &metrics.GatewaySource{BaseURL: cfg.GatewayAdminURL},
		Queue:   &metrics.QueueSource{BaseURL: cfg.EventsURL},
		Runtime: &metrics.RuntimeSource{BaseURL: cfg.RuntimeURL},
		Fake:    fake,
		Prefer:  cfg.MetricSourceMode,
	}

	ready := health.NewReadiness(db)
	mux := http.NewServeMux()
	health.NewHandler(ready).Register(mux)
	(&httpserver.Routes{Store: store, Hub: hub}).Register(mux)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ready.MarkReady()

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	loop := &evaluate.Loop{
		Store:    store,
		Source:   router,
		Interval: cfg.EvalInterval,
		Log:      log,
	}
	go loop.Run(bgCtx)

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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
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
