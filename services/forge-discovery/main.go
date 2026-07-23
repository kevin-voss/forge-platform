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

	"forge.local/services/forge-discovery/internal/config"
	"forge.local/services/forge-discovery/internal/controlmirror"
	"forge.local/services/forge-discovery/internal/httpapi"
	"forge.local/services/forge-discovery/internal/middleware"
	"forge.local/services/forge-discovery/internal/nodewatch"
	"forge.local/services/forge-discovery/internal/observability"
	"forge.local/services/forge-discovery/internal/store"
	"forge.local/services/forge-discovery/internal/sweeper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	otelCfg := observability.LoadConfig(cfg.ServiceName, cfg.ServiceVersion, cfg.Env)
	otelProvider := observability.Init(context.Background(), otelCfg)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		otelProvider.Shutdown(shutdownCtx)
	}()

	ctx, span := otelProvider.Tracer.Start(context.Background(), "discovery.startup",
		trace.WithAttributes(
			attribute.String("forge.service", cfg.ServiceName),
			attribute.String("db_schema", cfg.DatabaseSchema),
			attribute.String("control_url", cfg.ControlURL),
		),
	)
	defer span.End()

	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.DatabaseSchema, cfg.DatabasePoolMax, cfg.DatabaseMigrateOnStart)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mirror := controlmirror.NewWorker(cfg.ControlURL, log)
	go mirror.Run(bgCtx)

	ready := httpapi.NewReadiness(db)
	endpoints := &httpapi.EndpointsHandler{
		Store:        db,
		Log:          log,
		DefaultLease: cfg.LeaseSecondsDefault,
		Mirror:       mirror,
	}
	mux := httpapi.NewRouterWith(httpapi.RouterDeps{
		Ready:     ready,
		Endpoints: endpoints,
		Log:       log,
	})

	var handler http.Handler = mux
	handler = middleware.RequestID(middleware.DefaultRequestIDHeader)(handler)
	handler = observability.Middleware(otelProvider, log)(handler)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
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

	// Register kinds after listen so /health/live answers during Control backoff.
	regClient := controlmirror.New(cfg.ControlURL, log)
	regCtx, regCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer regCancel()
	if err := regClient.RegisterKinds(regCtx, 30*time.Second); err != nil {
		log.Error("kind registration failed; readiness will stay 503",
			"event", "discovery.kind_registered",
			"error", err.Error(),
		)
	} else {
		ready.MarkKindsRegistered()
	}

	sweep := &sweeper.Runner{
		Store: db,
		Log:   log,
		Cfg: sweeper.Config{
			Interval:  cfg.SweepInterval,
			ReapAfter: cfg.ReapAfter,
		},
		Mirror: mirror,
	}
	go sweep.Run(bgCtx)

	nodes := &nodewatch.Subscriber{
		Store: db,
		Log:   log,
		Cfg: nodewatch.Config{
			ControlURL:  cfg.ControlURL,
			ResyncEvery: cfg.NodeWatchResync,
		},
		Tracer: otelProvider.Tracer,
	}
	go nodes.Run(bgCtx)

	log.Info("discovery started",
		"event", "discovery.started",
		"port", cfg.Port,
		"db_schema", cfg.DatabaseSchema,
		"control_url", cfg.ControlURL,
		"otel_enabled", otelCfg.Enabled,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
		"auth_mode", cfg.AuthMode,
		"kinds_registered", ready.KindsRegistered(),
		"lease_seconds_default", cfg.LeaseSecondsDefault,
		"sweep_interval_seconds", int(cfg.SweepInterval.Seconds()),
		"reap_after_seconds", int(cfg.ReapAfter.Seconds()),
	)

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
