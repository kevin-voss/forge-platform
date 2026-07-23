// Package observability bootstraps OpenTelemetry for forge-discovery (step 12.02).
package observability

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
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

// Standard metric names (instrumentation checklist).
const (
	MetricServiceUp       = "forge_discovery_up"
	MetricHTTPRequests    = "forge_http_requests_total"
	MetricHTTPDurationSec = "forge_http_request_duration_seconds"
)

// Config controls OTEL bootstrap.
type Config struct {
	Enabled        bool
	Endpoint       string
	ServiceName    string
	ServiceVersion string
	Env            string
	ForgeService   string
}

// LoadConfig reads FORGE_OTEL_* / OTEL_* env vars.
func LoadConfig(serviceName, serviceVersion, env string) Config {
	enabled := true
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_OTEL_ENABLED")))
	switch raw {
	case "false", "0", "no":
		enabled = false
	}
	endpoint := strings.TrimSpace(os.Getenv("FORGE_OTEL_EXPORTER_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		endpoint = "http://otel-collector:4317"
	}
	if serviceName == "" {
		serviceName = "forge-discovery"
	}
	if serviceVersion == "" {
		serviceVersion = "0.1.0"
	}
	if env == "" {
		env = "development"
	}
	return Config{
		Enabled:        enabled,
		Endpoint:       endpoint,
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		Env:            env,
		ForgeService:   serviceName,
	}
}

// Provider holds SDK handles and standard instruments.
type Provider struct {
	enabled   bool
	tp        *sdktrace.TracerProvider
	mp        *sdkmetric.MeterProvider
	Tracer    trace.Tracer
	Requests  metric.Int64Counter
	Duration  metric.Float64Histogram
	serviceUp metric.Int64ObservableGauge
	upValue   atomic.Int64
}

// Enabled reports whether export is active.
func (p *Provider) Enabled() bool {
	return p != nil && p.enabled
}

// Init bootstraps OTEL. Export is asynchronous and fail-open: an unreachable
// collector never blocks request handling. Init errors degrade to no-op export.
func Init(ctx context.Context, cfg Config) *Provider {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	p := &Provider{}
	p.upValue.Store(1)

	if !cfg.Enabled {
		p.Tracer = otel.Tracer(cfg.ServiceName)
		return p
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.DeploymentEnvironment(cfg.Env),
			attribute.String("forge.service", cfg.ForgeService),
		),
	)
	if err != nil {
		p.Tracer = otel.Tracer(cfg.ServiceName)
		return p
	}

	endpoint := stripScheme(cfg.Endpoint)
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(2*time.Second),
	)
	if err != nil {
		p.Tracer = otel.Tracer(cfg.ServiceName)
		return p
	}
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(endpoint),
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithTimeout(2*time.Second),
	)
	if err != nil {
		_ = traceExp.Shutdown(ctx)
		p.Tracer = otel.Tracer(cfg.ServiceName)
		return p
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp,
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithExportTimeout(2*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(15*time.Second),
			sdkmetric.WithTimeout(2*time.Second),
		)),
		sdkmetric.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	meter := mp.Meter("forge.discovery")
	requests, _ := meter.Int64Counter(MetricHTTPRequests)
	duration, _ := meter.Float64Histogram(MetricHTTPDurationSec, metric.WithUnit("s"))
	versionAttr := attribute.String("version", cfg.ServiceVersion)
	up, _ := meter.Int64ObservableGauge(MetricServiceUp,
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(p.upValue.Load(), metric.WithAttributes(versionAttr))
			return nil
		}),
	)

	p.enabled = true
	p.tp = tp
	p.mp = mp
	p.Tracer = tp.Tracer("forge.discovery")
	p.Requests = requests
	p.Duration = duration
	p.serviceUp = up
	return p
}

// Shutdown flushes exporters with a short bound.
func (p *Provider) Shutdown(ctx context.Context) {
	if p == nil {
		return
	}
	p.upValue.Store(0)
	if p.tp != nil {
		_ = p.tp.Shutdown(ctx)
	}
	if p.mp != nil {
		_ = p.mp.Shutdown(ctx)
	}
}

// RecordHTTP records standard request metrics with bounded labels only.
func (p *Provider) RecordHTTP(ctx context.Context, method string, status int, seconds float64) {
	if p == nil || p.Requests == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("http_method", method),
		attribute.String("http_status_class", statusClass(status)),
	}
	p.Requests.Add(ctx, 1, metric.WithAttributes(attrs...))
	if p.Duration != nil {
		p.Duration.Record(ctx, seconds, metric.WithAttributes(attrs...))
	}
}

func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}

func stripScheme(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimPrefix(e, "http://")
	e = strings.TrimPrefix(e, "https://")
	return e
}

// ForbiddenMetricLabels are high-cardinality keys that must never appear on metrics.
var ForbiddenMetricLabels = []string{"request_id", "trace_id", "span_id", "path", "url"}
