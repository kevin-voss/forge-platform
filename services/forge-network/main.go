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

	"forge.local/services/forge-network/internal/api"
	"forge.local/services/forge-network/internal/config"
	"forge.local/services/forge-network/internal/db"
	"forge.local/services/forge-network/internal/docker"
	"forge.local/services/forge-network/internal/network"
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
	database, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabaseSchema, cfg.DatabasePoolMax, cfg.DatabaseMigrateOnStart)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer database.Close()

	var dockerSrc network.SubnetSource
	if cfg.DockerCollisionCheck {
		cli, err := docker.New(cfg.DockerHost)
		if err != nil {
			log.Warn("docker client init failed", "error", err.Error())
		} else {
			dockerSrc = cli
		}
	}

	alloc := &network.Allocator{
		Pool:          database.Pool,
		Log:           log,
		Docker:        dockerSrc,
		ProviderCIDRs: cfg.ProviderCIDRs,
		SkipDocker:    !cfg.DockerCollisionCheck,
	}
	if err := alloc.MarkFailedNetworks(ctx); err != nil {
		log.Warn("startup collision scan failed", "error", err.Error())
	}
	if err := alloc.CheckCollision(ctx, cfg.ClusterCIDR); err != nil {
		log.Error("configured cluster CIDR collides with Docker/provider network",
			"cluster_cidr", cfg.ClusterCIDR,
			"reason", err.Error(),
		)
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	go runReclaimer(bgCtx, alloc, cfg.LeaseReclaimInterval, log)

	mux := api.NewRouter(api.Deps{Alloc: alloc, DB: database, Log: log})
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

	log.Info("network started",
		"event", "network.started",
		"port", cfg.Port,
		"db_schema", cfg.DatabaseSchema,
		"cluster_cidr", cfg.ClusterCIDR,
		"node_prefix_length", cfg.NodePrefixLength,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
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

func runReclaimer(ctx context.Context, alloc *network.Allocator, every time.Duration, log *slog.Logger) {
	if every <= 0 {
		every = 60 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := alloc.ReclaimOrphans(ctx)
			if err != nil {
				log.Warn("orphan reclaim failed", "error", err.Error())
				continue
			}
			if n > 0 {
				log.Info("orphan reclaim complete", "released", n)
			}
		}
	}
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
