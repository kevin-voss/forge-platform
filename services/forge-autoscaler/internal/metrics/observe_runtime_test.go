package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

func TestObserveSourceCPUQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"value": []any{1.0, "82.5"}},
				},
			},
		})
	}))
	defer srv.Close()

	src := &metrics.ObserveSource{BaseURL: srv.URL}
	util := 65.0
	sample, err := src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "invoice-api"},
		policy.MetricSpec{Type: "cpu", TargetAverageUtilization: &util},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if sample.Value != 82.5 || sample.Source != "observe" {
		t.Fatalf("sample=%+v", sample)
	}
}

func TestRuntimeSourceFallbackEstimate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/node":
			_ = json.NewEncoder(w).Encode(map[string]any{"cpu": 2, "memoryBytes": 2 * 1024 * 1024 * 1024})
		case "/v1/node/state":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workloads": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := &metrics.RuntimeSource{BaseURL: srv.URL}
	util := 65.0
	sample, err := src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "demo"},
		policy.MetricSpec{Type: "cpu", TargetAverageUtilization: &util},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if sample.Source != "runtime" || sample.Value <= 0 {
		t.Fatalf("sample=%+v", sample)
	}
}

func TestRouterFallsBackToRuntime(t *testing.T) {
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/node":
			_ = json.NewEncoder(w).Encode(map[string]any{"cpu": 4, "memoryBytes": 8 * 1024 * 1024 * 1024})
		case "/v1/node/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"workloads": []any{map[string]any{"id": "w1"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer runtime.Close()

	observe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer observe.Close()

	router := &metrics.Router{
		Observe: &metrics.ObserveSource{BaseURL: observe.URL},
		Runtime: &metrics.RuntimeSource{BaseURL: runtime.URL},
	}
	util := 65.0
	sample, err := router.Fetch(context.Background(),
		policy.TargetRef{Kind: "Application", Name: "demo"},
		policy.MetricSpec{Type: "cpu", TargetAverageUtilization: &util},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if sample.Source != "runtime" {
		t.Fatalf("expected runtime fallback, got %s", sample.Source)
	}
}
