package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

type otelHandle struct {
	enabled bool
	tp      *sdktrace.TracerProvider
	tracer  trace.Tracer
}

func initOTEL(ctx context.Context, cfg config, log *slog.Logger) *otelHandle {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	h := &otelHandle{tracer: otel.Tracer(cfg.ServiceName)}
	if !cfg.OTELEnabled {
		log.Info("otel disabled")
		return h
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.DeploymentEnvironment(cfg.Env),
			attribute.String("forge.service", cfg.ServiceName),
		),
	)
	if err != nil {
		log.Warn("otel resource failed; continuing without export", "error", err.Error())
		return h
	}

	endpoint := stripScheme(cfg.OTELEndpoint)
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(2*time.Second),
	)
	if err != nil {
		log.Warn("otel exporter init failed; continuing without export", "error", err.Error())
		return h
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(1*time.Second),
			sdktrace.WithExportTimeout(2*time.Second),
			sdktrace.WithMaxExportBatchSize(64),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	h.enabled = true
	h.tp = tp
	h.tracer = tp.Tracer("demo.app")
	log.Info("otel enabled", "endpoint", cfg.OTELEndpoint)
	return h
}

func (h *otelHandle) Shutdown(ctx context.Context) {
	if h == nil || h.tp == nil {
		return
	}
	_ = h.tp.Shutdown(ctx)
}

func stripScheme(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimPrefix(e, "http://")
	e = strings.TrimPrefix(e, "https://")
	return e
}
