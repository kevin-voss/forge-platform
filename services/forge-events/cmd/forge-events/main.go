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

	"forge.local/services/forge-events/internal/api"
	"forge.local/services/forge-events/internal/config"
	"forge.local/services/forge-events/internal/consumers"
	"forge.local/services/forge-events/internal/dlq"
	"forge.local/services/forge-events/internal/events"
	"forge.local/services/forge-events/internal/health"
	natsx "forge.local/services/forge-events/internal/nats"
	"forge.local/services/forge-events/internal/schema"
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
	log.Info("starting forge-events",
		"port", cfg.Port,
		"version", cfg.ServiceVersion,
		"env", cfg.Env,
		"log_level", cfg.LogLevel,
		"nats_url", cfg.NATSURL,
		"streams", strings.Join(cfg.Streams, ","),
		"event_max_bytes", cfg.EventMaxBytes,
		"consume_max_batch", cfg.ConsumeMaxBatch,
		"consume_wait_ms", int(cfg.ConsumeWait.Milliseconds()),
		"default_ack_wait_s", cfg.DefaultAckWaitS,
		"default_max_deliveries", cfg.DefaultMaxDeliveries,
		"ack_token_ttl_s", cfg.AckTokenTTLS,
		"dlq_enabled", cfg.DLQEnabled,
		"dlq_retention_days", cfg.DLQRetentionDays,
		"event_schema_dir", cfg.EventSchemaDir,
		"schema_validation", cfg.SchemaValidation,
		"shutdown_grace_seconds", int(cfg.ShutdownGrace.Seconds()),
	)

	schemaMetrics := &schema.Metrics{}
	schemaReg := schema.NewRegistry(schema.Mode(cfg.SchemaValidation), log, schemaMetrics)
	if err := schemaReg.Load(cfg.EventSchemaDir); err != nil {
		// Keep serving so /health/live works; readiness stays 503 until schemas load.
		log.Error("event schema load failed", "error", err.Error(), "dir", cfg.EventSchemaDir)
	}

	metrics := &natsx.Metrics{}
	conn := natsx.NewConnWithDLQ(cfg.NATSURL, cfg.Streams, cfg.DLQEnabled, log, metrics)
	if err := conn.Connect(context.Background()); err != nil {
		return err
	}
	defer func() {
		if err := conn.Drain(); err != nil {
			log.Warn("nats drain", "error", err.Error())
			conn.Close()
		}
	}()

	eventMetrics := &events.Metrics{}
	publisher := events.NewPublisher(conn.JetStream(), cfg.Streams, cfg.EventMaxBytes, log, eventMetrics)
	publisher.SetSchemaValidator(schemaReg)
	ackMetrics := &consumers.AckMetrics{}
	ackMgr := consumers.NewAckManager(time.Duration(cfg.AckTokenTTLS)*time.Second, log, ackMetrics)
	store := consumers.NewStore(
		conn.JetStream(),
		cfg.Streams,
		cfg.DefaultAckWaitS,
		cfg.DefaultMaxDeliveries,
		cfg.ConsumeMaxBatch,
		cfg.ConsumeWait,
		ackMgr,
		log,
		&consumers.Metrics{},
	)

	dlqMetrics := &dlq.Metrics{}
	dlqStore := dlq.NewStore(conn.JetStream())
	dlqRouter := dlq.NewRouter(conn.JetStream(), dlqStore, cfg.DLQEnabled, log, dlqMetrics)
	store.SetDLQRouter(dlqRouter)
	redeliverer := dlq.NewRedeliverer(conn.JetStream(), dlqStore, log, dlqMetrics)
	retention := dlq.NewRetentionRunner(dlqStore, dlqRouter, cfg.DLQRetentionDays, log, dlqMetrics)

	mux := http.NewServeMux()
	ready := health.MultiReady{conn, schemaReg}
	health.NewHandler(ready, cfg.ServiceName, cfg.ServiceVersion).Register(mux)
	(&api.PublishHandler{Publisher: publisher, MaxBytes: cfg.EventMaxBytes}).Register(mux)
	(&api.ConsumeHandler{Consumer: store, MaxBytes: cfg.EventMaxBytes, Wait: cfg.ConsumeWait}).Register(mux)
	(&api.ConsumersHandler{Store: store, Acker: ackMgr, MaxBytes: cfg.EventMaxBytes}).Register(mux)
	(&api.DLQHandler{Store: dlqStore, Redeliverer: redeliverer, Enabled: cfg.DLQEnabled}).Register(mux)
	(&api.SchemasHandler{Registry: schemaReg}).Register(mux)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	go retention.Run(bgCtx)

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
	log.Info("shutdown complete",
		"forge_events_ready", metrics.Ready.Load(),
		"forge_nats_reconnects_total", metrics.Reconnects.Load(),
		"forge_streams_total", metrics.Streams.Load(),
	)
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
