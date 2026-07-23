package metrics_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

func TestGatewaySourceHTTPRequestsAndConnections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/metrics" || r.URL.Query().Get("application") != "invoice-api" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"application":       "invoice-api",
			"requestsPerSecond": 450.0,
			"activeConnections": 80.0,
			"sampleCount":       1200,
		})
	}))
	defer srv.Close()

	tel := telemetry.NewRegistry()
	src := &metrics.GatewaySource{BaseURL: srv.URL, Metrics: tel}
	target := 150.0
	sample, err := src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		policy.MetricSpec{Type: "httpRequests", TargetValue: &target},
	)
	if err != nil {
		t.Fatalf("Fetch httpRequests: %v", err)
	}
	if sample.Value != 450 || sample.Source != "gateway" || sample.SampleCount != 1200 {
		t.Fatalf("sample=%+v", sample)
	}

	connTarget := 40.0
	sample, err = src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		policy.MetricSpec{Type: "activeConnections", TargetValue: &connTarget},
	)
	if err != nil {
		t.Fatalf("Fetch activeConnections: %v", err)
	}
	if sample.Value != 80 {
		t.Fatalf("connections=%+v", sample)
	}
}

func TestGatewaySourceMissingRouteUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	src := &metrics.GatewaySource{BaseURL: srv.URL}
	target := 150.0
	_, err := src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "missing"},
		policy.MetricSpec{Type: "httpRequests", TargetValue: &target},
	)
	if err == nil || !errors.Is(err, metrics.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestObserveSourceLatencyAndErrorRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		var value string
		switch {
		case strings.Contains(q, "histogram_quantile"):
			value = "0.250"
		case strings.Contains(q, "http_status_class"):
			value = "0.08"
		case strings.Contains(q, "increase("):
			value = "200"
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"value": []any{1.0, value}},
				},
			},
		})
	}))
	defer srv.Close()

	src := &metrics.ObserveSource{BaseURL: srv.URL}
	latTarget := 0.20
	sample, err := src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		policy.MetricSpec{Type: "p95Latency", TargetValue: &latTarget},
	)
	if err != nil {
		t.Fatalf("p95: %v", err)
	}
	if sample.Value != 0.250 || sample.SampleCount != 200 || sample.Source != "observe" {
		t.Fatalf("sample=%+v", sample)
	}

	errTarget := 0.05
	sample, err = src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		policy.MetricSpec{Type: "errorRate", TargetValue: &errTarget},
	)
	if err != nil {
		t.Fatalf("errorRate: %v", err)
	}
	if sample.Value != 0.08 {
		t.Fatalf("errorRate sample=%+v", sample)
	}
}

func TestRouterGatewayFallsBackToObserve(t *testing.T) {
	observe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"value": []any{1.0, "300"}},
				},
			},
		})
	}))
	defer observe.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer gateway.Close()

	router := &metrics.Router{
		Gateway: &metrics.GatewaySource{BaseURL: gateway.URL},
		Observe: &metrics.ObserveSource{BaseURL: observe.URL},
	}
	target := 150.0
	sample, err := router.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		policy.MetricSpec{Type: "httpRequests", TargetValue: &target},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if sample.Source != "observe" || sample.Value != 300 {
		t.Fatalf("expected observe fallback, got %+v", sample)
	}
}
