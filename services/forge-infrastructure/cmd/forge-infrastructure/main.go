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

	"forge.local/services/forge-infrastructure/internal/api"
	"forge.local/services/forge-infrastructure/internal/config"
	"forge.local/services/forge-infrastructure/internal/controller"
	"forge.local/services/forge-infrastructure/internal/health"
	"forge.local/services/forge-infrastructure/internal/operations"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/noop"
	"forge.local/services/forge-infrastructure/internal/registryclient"
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

	ctx := context.Background()
	db, err := operations.Open(ctx, cfg.DatabaseURL, cfg.DatabaseSchema, cfg.DatabasePoolMax, cfg.DatabaseMigrateOnStart)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	ledger := &operations.Ledger{
		Pool:   db.Pool,
		IDs:    operations.NewGenerator(),
		Schema: cfg.DatabaseSchema,
	}

	reg := provider.NewRegistry(noop.Factory)
	// Known type keys reserved; real adapters register in 23.02+.
	_ = provider.TypeDocker

	registryClient := registryclient.New(cfg.RegistryURL, log)
	ready := health.NewReadiness(db)

	mux := http.NewServeMux()
	health.NewHandler(ready).Register(mux)
	(&api.Handler{Ledger: ledger}).Register(mux)

	ctrl := &controller.NodePoolController{
		Registry:  registryClient,
		Ledger:    ledger,
		Providers: reg,
		Log:       log,
		Interval:  cfg.ReconcileInterval,
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			errCh <- err
			return
		}
		log.Info("infrastructure listening",
			"event", "infra.started",
			"port", cfg.Port,
			"registry_url", cfg.RegistryURL,
			"db_schema", cfg.DatabaseSchema,
		)
		errCh <- srv.Serve(ln)
	}()

	// Register kinds after listen so /health/live answers during Control backoff.
	regCtx, regCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer regCancel()
	if err := registryClient.RegisterKinds(regCtx, 30*time.Second); err != nil {
		log.Error("kind registration failed; readiness will stay 503",
			"event", "infra.kind_registered",
			"error", err.Error(),
		)
	} else {
		ready.MarkKindsRegistered()
		go ctrl.Run(bgCtx)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		log.Info("shutdown signal", "signal", sig.String())
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	bgCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func newLogger(service, level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: "timestamp", Value: slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))}
			}
			return a
		},
	})
	return slog.New(h).With("service", service)
}
