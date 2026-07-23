package observability_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-gateway/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPropagatesInboundTraceparent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Build a valid parent context and serialize to header.
	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	carrier := propagation.HeaderCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	parent.End()
	tpHeader := carrier.Get("traceparent")
	if tpHeader == "" {
		t.Fatal("expected traceparent from parent span")
	}
	wantTrace := strings.Split(tpHeader, "-")[1]

	var outboundTP string
	h := observability.Middleware(nil, slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outboundTP = r.Header.Get("traceparent")
		observability.InjectHeaders(r)
		outboundTP = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("traceparent", tpHeader)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if outboundTP == "" {
		t.Fatal("expected outbound traceparent")
	}
	gotTrace := strings.Split(outboundTP, "-")[1]
	if gotTrace != wantTrace {
		t.Fatalf("trace id=%q want %q (header=%q)", gotTrace, wantTrace, outboundTP)
	}
	if rr.Header().Get(observability.HeaderForgeRequestID) == "" {
		t.Fatal("expected X-Forge-Request-ID on response")
	}
}

func TestMalformedTraceparentStartsNewRoot(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var gotTP string
	h := observability.Middleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTP = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("traceparent", "not-a-valid-traceparent")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if gotTP == "" {
		t.Fatal("middleware should mint a new traceparent for malformed inbound")
	}
	parts := strings.Split(gotTP, "-")
	if len(parts) != 4 || parts[1] == "00000000000000000000000000000000" {
		t.Fatalf("unexpected new traceparent %q", gotTP)
	}
}

func TestLogEnricherFields(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	h := observability.Middleware(nil, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/echo", nil)
	req.Header.Set(observability.HeaderForgeRequestID, "req_logtest")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	line := buf.String()
	if !strings.Contains(line, `"request_id":"req_logtest"`) {
		t.Fatalf("missing request_id in log: %s", line)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &payload); err != nil {
		t.Fatalf("log json: %v (%s)", err, line)
	}
	if payload["trace_id"] == nil || payload["trace_id"] == "" {
		t.Fatalf("missing trace_id: %s", line)
	}
	if payload["span_id"] == nil || payload["span_id"] == "" {
		t.Fatalf("missing span_id: %s", line)
	}
}

func TestMetricLabelCardinalityLint(t *testing.T) {
	for _, forbidden := range observability.ForbiddenMetricLabels {
		switch forbidden {
		case "request_id", "trace_id", "span_id", "path", "url":
			// ok — documented forbidden set
		default:
			t.Fatalf("unexpected forbidden label %q", forbidden)
		}
	}
	// Ensure standard metric names stay stable.
	if observability.MetricHTTPRequests != "forge_http_requests_total" {
		t.Fatal(observability.MetricHTTPRequests)
	}
	if observability.MetricHTTPDurationSec != "forge_http_request_duration_seconds" {
		t.Fatal(observability.MetricHTTPDurationSec)
	}
	if observability.MetricServiceUp != "forge_service_up" {
		t.Fatal(observability.MetricServiceUp)
	}
}

func TestFailOpenInitUnreachableCollector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := observability.Init(ctx, observability.Config{
		Enabled:     true,
		Endpoint:    "http://127.0.0.1:1",
		ServiceName: "forge-gateway",
		Env:         "test",
	})
	if p == nil {
		t.Fatal("Init must return a provider even when collector is down")
	}
	// Serving must not panic with this provider.
	h := observability.Middleware(p, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	p.Shutdown(shutdownCtx)
}
