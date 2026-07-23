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
	discoverydns "forge.local/services/forge-discovery/internal/dns"
	"forge.local/services/forge-discovery/internal/httpapi"
	"forge.local/services/forge-discovery/internal/middleware"
	"forge.local/services/forge-discovery/internal/nodewatch"
	"forge.local/services/forge-discovery/internal/observability"
	"forge.local/services/forge-discovery/internal/store"
	"forge.local/services/forge-discovery/internal/sweeper"
	"forge.local/services/forge-discovery/internal/watchhub"
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

	broker := watchhub.New(watchhub.Config{
		BufferSize:     cfg.WatchBufferSize,
		MaxConnections: cfg.WatchMaxConnections,
	})
	ready := httpapi.NewReadiness(db)
	endpoints := &httpapi.EndpointsHandler{
		Store:          db,
		Log:            log,
		DefaultLease:   cfg.LeaseSecondsDefault,
		Mirror:         mirror,
		Watch:          broker,
		WatchHeartbeat: cfg.WatchHeartbeat,
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

	errCh := make(chan error, 2)
	go func() {
		log.Info("listening", "addr", ln.Addr().String())
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	var dnsServer *discoverydns.Server
	if cfg.DNSEnabled {
		var overlayFilter discoverydns.OverlayFilter = discoverydns.PublicIPRejectFilter{}
		if cfg.NetworkURL != "" {
			cidr, err := discoverydns.ParseOverlayCIDR(cfg.OverlayCIDR)
			if err != nil {
				return fmt.Errorf("overlay cidr: %w", err)
			}
			leaseIdx := &discoverydns.NetworkLeaseIndex{
				BaseURL:      cfg.NetworkURL,
				NetworkName:  cfg.NetworkName,
				RefreshEvery: cfg.OverlayLeaseRefresh,
			}
			go leaseIdx.Run(bgCtx)
			overlayFilter = &discoverydns.CIDROverlayFilter{
				OverlayCIDR:  cidr,
				LeaseChecker: leaseIdx,
			}
			log.Info("dns overlay lease filter enabled",
				"overlay_cidr", cfg.OverlayCIDR,
				"network_url", cfg.NetworkURL,
				"network_name", cfg.NetworkName,
			)
		}
		dnsServer = &discoverydns.Server{
			Addr: fmt.Sprintf(":%d", cfg.DNSPort),
			Zone: cfg.DNSZone,
			Resolver: &discoverydns.ZoneResolver{
				Store: db,
				Zone:  cfg.DNSZone,
				TTL: discoverydns.TTLPolicy{
					MaxTTL:      time.Duration(cfg.DNSTTLSeconds) * time.Second,
					NegativeTTL: time.Duration(cfg.DNSNegativeTTLSeconds) * time.Second,
				},
				Overlay: overlayFilter,
			},
			Forwarder: &discoverydns.Forwarder{
				Upstream: cfg.DNSForwardUpstream,
				Timeout:  cfg.DNSForwardTimeout,
			},
			Log:           log,
			Tracer:        otelProvider.Tracer,
			QueriesTotal:  otelProvider.DNSQueries,
			NXDomainTotal: otelProvider.DNSNXDomain,
			ForwardErrors: otelProvider.DNSForwardErr,
		}
		ready.SetDNS(dnsServer)
		go func() {
			log.Info("dns listening", "addr", dnsServer.Addr, "zone", cfg.DNSZone)
			if err := dnsServer.ListenAndServe(); err != nil {
				errCh <- fmt.Errorf("dns: %w", err)
			}
		}()
		// Brief wait so /health/ready can see the UDP bind.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !dnsServer.IsBound() {
			time.Sleep(20 * time.Millisecond)
		}
	}

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
		Watch:  endpoints,
	}
	go sweep.Run(bgCtx)

	nodes := &nodewatch.Subscriber{
		Store: db,
		Log:   log,
		Cfg: nodewatch.Config{
			ControlURL:  cfg.ControlURL,
			ResyncEvery: cfg.NodeWatchResync,
		},
		Watch:  endpoints,
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
		"watch_buffer_size", cfg.WatchBufferSize,
		"watch_max_connections", cfg.WatchMaxConnections,
		"watch_heartbeat_seconds", int(cfg.WatchHeartbeat.Seconds()),
		"dns_enabled", cfg.DNSEnabled,
		"dns_port", cfg.DNSPort,
		"dns_zone", cfg.DNSZone,
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-errCh:
		bgCancel()
		if dnsServer != nil {
			_ = dnsServer.Shutdown()
		}
		return err
	case sig := <-sigCh:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	bgCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if dnsServer != nil {
		_ = dnsServer.Shutdown()
	}
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
