package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	metricHTTPRequests    = "forge_http_requests_total"
	metricHTTPDurationSec = "forge_http_request_duration_seconds"
)

type otelHandle struct {
	enabled  bool
	tp       *sdktrace.TracerProvider
	mp       *sdkmetric.MeterProvider
	tracer   trace.Tracer
	requests metric.Int64Counter
	duration metric.Float64Histogram

	mu        sync.Mutex
	latencies []float64 // seconds, ring for local p95 publish
}

func initOTEL(ctx context.Context, log *slog.Logger) *otelHandle {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	serviceName := strings.TrimSpace(os.Getenv("FORGE_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = "pulseboard-api"
	}
	envName := strings.TrimSpace(os.Getenv("FORGE_ENV"))
	if envName == "" {
		envName = "local"
	}

	h := &otelHandle{
		tracer: otel.Tracer(serviceName),
	}

	enabled := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_OTEL_ENABLED"))) {
	case "false", "0", "no":
		enabled = false
	}
	if !enabled {
		log.Info("otel disabled")
		return h
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.DeploymentEnvironment(envName),
			attribute.String("forge.service", serviceName),
			attribute.String("application", serviceName),
		),
	)
	if err != nil {
		log.Warn("otel resource failed; continuing without export", "error", err.Error())
		return h
	}

	endpoint := strings.TrimSpace(os.Getenv("FORGE_OTEL_EXPORTER_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		endpoint = "http://otel-collector:4317"
	}
	ep := stripScheme(endpoint)

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(ep),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(2*time.Second),
	)
	if err != nil {
		log.Warn("otel trace exporter init failed; continuing without export", "error", err.Error())
		return h
	}
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(ep),
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithTimeout(2*time.Second),
	)
	if err != nil {
		log.Warn("otel metric exporter init failed; continuing without export", "error", err.Error())
		return h
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp,
			sdktrace.WithBatchTimeout(1*time.Second),
			sdktrace.WithExportTimeout(2*time.Second),
			sdktrace.WithMaxExportBatchSize(64),
		),
		sdktrace.WithResource(res),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(5*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	meter := mp.Meter("pulseboard-api")
	requests, err := meter.Int64Counter(metricHTTPRequests)
	if err != nil {
		log.Warn("otel requests counter failed", "error", err.Error())
	}
	duration, err := meter.Float64Histogram(metricHTTPDurationSec,
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Warn("otel duration histogram failed", "error", err.Error())
	}

	h.enabled = true
	h.tp = tp
	h.mp = mp
	h.tracer = tp.Tracer("pulseboard-api")
	h.requests = requests
	h.duration = duration
	log.Info("otel enabled", "endpoint", endpoint, "service", serviceName)
	return h
}

func (h *otelHandle) Shutdown(ctx context.Context) {
	if h == nil {
		return
	}
	if h.tp != nil {
		_ = h.tp.Shutdown(ctx)
	}
	if h.mp != nil {
		_ = h.mp.Shutdown(ctx)
	}
}

func (h *otelHandle) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		ctx := r.Context()
		spanName := r.Method + " " + r.URL.Path
		ctx, span := h.tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		next.ServeHTTP(ww, r.WithContext(ctx))

		elapsed := time.Since(start).Seconds()
		h.recordLocalLatency(elapsed)

		attrs := []attribute.KeyValue{
			attribute.String("http_method", r.Method),
			attribute.String("http_status_class", statusClass(ww.status)),
			attribute.String("application", "pulseboard-api"),
		}
		if h.enabled {
			if h.requests != nil {
				h.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
			}
			if h.duration != nil {
				h.duration.Record(ctx, elapsed, metric.WithAttributes(attrs...))
			}
		}
		span.SetAttributes(attrs...)
	})
}

func (h *otelHandle) recordLocalLatency(seconds float64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latencies = append(h.latencies, seconds)
	if len(h.latencies) > 256 {
		h.latencies = h.latencies[len(h.latencies)-256:]
	}
}

func (h *otelHandle) LocalP95Seconds() float64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.latencies)
	if n == 0 {
		return 0
	}
	cp := append([]float64(nil), h.latencies...)
	// insertion sort — n ≤ 256
	for i := 1; i < len(cp); i++ {
		v := cp[i]
		j := i - 1
		for j >= 0 && cp[j] > v {
			cp[j+1] = cp[j]
			j--
		}
		cp[j+1] = v
	}
	idx := int(float64(n-1) * 0.95)
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return cp[idx]
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

func stripScheme(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimPrefix(e, "http://")
	e = strings.TrimPrefix(e, "https://")
	return e
}
