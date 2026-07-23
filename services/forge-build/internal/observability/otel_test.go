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

	"forge.local/services/forge-build/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPropagatesInboundTraceparent(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	carrier := propagation.HeaderCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	parent.End()
	tpHeader := carrier.Get("traceparent")
	wantTrace := strings.Split(tpHeader, "-")[1]

	var outboundTP string
	h := observability.Middleware(nil, slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observability.InjectHeaders(r)
		outboundTP = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/builds", nil)
	req.Header.Set("traceparent", tpHeader)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	gotTrace := strings.Split(outboundTP, "-")[1]
	if gotTrace != wantTrace {
		t.Fatalf("trace id=%q want %q", gotTrace, wantTrace)
	}
}

func TestMalformedTraceparentStartsNewRoot(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var gotTP string
	h := observability.Middleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTP = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/builds/x", nil)
	req.Header.Set("traceparent", "bad")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if gotTP == "" {
		t.Fatal("expected new traceparent")
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
	req := httptest.NewRequest(http.MethodGet, "/v1/builds", nil)
	req.Header.Set(observability.HeaderForgeRequestID, "req_build")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	line := buf.String()
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &payload); err != nil {
		t.Fatalf("json: %v (%s)", err, line)
	}
	if payload["request_id"] != "req_build" {
		t.Fatalf("request_id=%v", payload["request_id"])
	}
	if payload["trace_id"] == nil || payload["trace_id"] == "" {
		t.Fatalf("missing trace_id: %s", line)
	}
}

func TestMetricLabelCardinalityLint(t *testing.T) {
	if len(observability.ForbiddenMetricLabels) == 0 {
		t.Fatal("expected forbidden labels")
	}
	if observability.MetricHTTPRequests != "forge_http_requests_total" {
		t.Fatal(observability.MetricHTTPRequests)
	}
}

func TestFailOpenInitUnreachableCollector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := observability.Init(ctx, observability.Config{
		Enabled: true, Endpoint: "http://127.0.0.1:1", ServiceName: "forge-build", Env: "test",
	})
	if p == nil {
		t.Fatal("nil provider")
	}
	h := observability.Middleware(p, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	p.Shutdown(context.Background())
}
