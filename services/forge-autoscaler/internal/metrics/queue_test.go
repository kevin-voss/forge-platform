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

func TestQueueSourceFetchesDepthAndOldestAge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/metrics" || r.URL.Query().Get("queue") != "invoice-jobs" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"queue":            "invoice-jobs",
			"depth":            20000,
			"oldestAgeSeconds": 45.5,
			"retryRate":        0.01,
			"consumerLag":      1200,
		})
	}))
	defer srv.Close()

	tel := telemetry.NewRegistry()
	src := &metrics.QueueSource{BaseURL: srv.URL, Metrics: tel}
	target := policy.TargetRef{Kind: "Worker", Name: "invoice-worker"}
	depthTarget := 500.0
	sample, err := src.Fetch(context.Background(), target, policy.MetricSpec{
		Type: "queueDepth", TargetValue: &depthTarget, Queue: "invoice-jobs",
	})
	if err != nil {
		t.Fatalf("Fetch depth: %v", err)
	}
	if sample.Value != 20000 || sample.Source != "queue" || sample.QueueName != "invoice-jobs" {
		t.Fatalf("unexpected depth sample: %+v", sample)
	}

	ageTarget := 30.0
	age, err := src.Fetch(context.Background(), target, policy.MetricSpec{
		Type: "oldestMessageAge", TargetValue: &ageTarget, Queue: "invoice-jobs",
	})
	if err != nil {
		t.Fatalf("Fetch age: %v", err)
	}
	if age.Value != 45.5 {
		t.Fatalf("oldest age=%v", age.Value)
	}

	rec := httptest.NewRecorder()
	tel.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `forge_autoscaler_queue_backlog{queue="invoice-jobs"} 20000`) {
		t.Fatalf("missing backlog metric:\n%s", body)
	}
	if !strings.Contains(body, `forge_autoscaler_metric_source_latency_seconds{source="queue"}`) {
		t.Fatalf("missing queue latency metric:\n%s", body)
	}
}

func TestQueueSourceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := &metrics.QueueSource{BaseURL: srv.URL}
	_, err := src.Fetch(context.Background(),
		policy.TargetRef{Kind: "Worker", Name: "missing"},
		policy.MetricSpec{Type: "queueDepth", Queue: "missing"},
	)
	if !errors.Is(err, metrics.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestQueueNameFallback(t *testing.T) {
	target := policy.TargetRef{Kind: "Worker", Name: "invoice-worker"}
	if got := metrics.QueueName(target, policy.MetricSpec{Queue: "explicit"}); got != "explicit" {
		t.Fatalf("queue field: %q", got)
	}
	if got := metrics.QueueName(target, policy.MetricSpec{Query: "from-query"}); got != "from-query" {
		t.Fatalf("query field: %q", got)
	}
	if got := metrics.QueueName(target, policy.MetricSpec{}); got != "invoice-worker" {
		t.Fatalf("target name: %q", got)
	}
}
