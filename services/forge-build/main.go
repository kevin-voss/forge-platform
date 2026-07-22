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

	"forge.local/services/forge-build/internal/api"
	"forge.local/services/forge-build/internal/builder"
	"forge.local/services/forge-build/internal/config"
	"forge.local/services/forge-build/internal/control"
	"forge.local/services/forge-build/internal/docker"
	"forge.local/services/forge-build/internal/health"
	"forge.local/services/forge-build/internal/jobs"
	"forge.local/services/forge-build/internal/registry"
	"forge.local/services/forge-build/internal/store"
	"forge.local/services/forge-build/internal/workspace"
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
	log.Info("starting forge-build",
		"port", cfg.Port,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
		"auth_mode", cfg.AuthMode,
		"log_level", cfg.LogLevel,
		"docker_host", cfg.DockerHost,
		"workspace_dir", cfg.WorkspaceDir,
		"store_dir", cfg.StoreDir,
		"retention_hours", int(cfg.Retention.Hours()),
		"cleanup_on_start", cfg.CleanupOnStart,
		"build_timeout_seconds", int(cfg.BuildTimeout.Seconds()),
		"max_concurrency", cfg.MaxConcurrency,
		"log_buffer_lines", cfg.LogBufferLines,
		"registry", cfg.Registry,
		"image_name_pattern", cfg.ImageNamePattern,
		"default_project", cfg.DefaultProject,
		"push_latest", cfg.PushLatest,
		"push_retries", cfg.PushRetries,
		"control_url", cfg.ControlURL,
		"auto_deploy_default", cfg.AutoDeployDefault,
		"control_retries", cfg.ControlRetries,
		"shutdown_grace_seconds", int(cfg.ShutdownGrace.Seconds()),
	)

	ws, err := workspace.New(cfg.WorkspaceDir)
	if err != nil {
		return err
	}

	st, err := store.New(cfg.StoreDir)
	if err != nil {
		return err
	}

	engine, err := docker.New(cfg.DockerHost)
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close() }()

	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	version, err := docker.StartupPing(startupCtx, engine, cfg.DockerStartupRetries, cfg.DockerStartupRetryDelay)
	startupCancel()
	if err != nil {
		log.Warn("docker unreachable after startup retries; continuing with readiness=503",
			"error", err.Error(),
			"workspace_dir", ws.Root(),
		)
	} else {
		log.Info("docker engine version recorded at startup",
			"docker_engine_version", version,
			"workspace_dir", ws.Root(),
		)
	}

	publisher := registry.New(engine, cfg.PushRetries, log)
	ctrlClient := control.New(cfg.ControlURL, &http.Client{Timeout: cfg.ControlTimeout})
	jobMgr := jobs.NewWithControl(jobs.Config{
		MaxConcurrency:      cfg.MaxConcurrency,
		BuildTimeout:        cfg.BuildTimeout,
		LogBufferLines:      cfg.LogBufferLines,
		DefaultForgeYAML:    cfg.DefaultForgeYAML,
		Registry:            cfg.Registry,
		ImageNamePattern:    cfg.ImageNamePattern,
		DefaultProject:      cfg.DefaultProject,
		PushLatest:          cfg.PushLatest,
		Retention:           cfg.Retention,
		CleanupOnStart:      cfg.CleanupOnStart,
		ControlRetries:      cfg.ControlRetries,
		ControlRetryBackoff: cfg.ControlRetryBackoff,
		ControlTimeout:      cfg.ControlTimeout,
	}, ws, builder.New(engine), publisher, ctrlClient, st, log)
	if err := jobMgr.Recover(); err != nil {
		return fmt.Errorf("recover build store: %w", err)
	}
	jobMgr.Start()
	defer jobMgr.Stop()

	mux := http.NewServeMux()
	health.NewHandler(engine).Register(mux)
	api.NewBuildHandlerWithDefaults(jobMgr, cfg.DefaultForgeYAML, cfg.AutoDeployDefault).Register(mux)
	api.NewStatusHandler(jobMgr).Register(mux)

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
