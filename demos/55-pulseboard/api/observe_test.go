package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObserveClientFetchPlatformStats(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		var value string
		switch {
		case strings.Contains(q, "forge_replicas_ready"):
			value = "3"
		case strings.Contains(q, "rate") && strings.Contains(q, "forge_http_requests_total"):
			value = "42.5"
		case strings.Contains(q, "histogram_quantile") || strings.Contains(q, `quantile="0.95"`):
			value = "0.085"
		default:
			http.Error(w, "unexpected query: "+q, http.StatusBadRequest)
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
	})
	mux.HandleFunc("/v1/metrics/query", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client := &ObserveClient{
		BaseURL:    ts.URL,
		App:        "pulseboard-api",
		HTTPClient: ts.Client(),
	}
	stats, err := client.FetchPlatformStats(context.Background())
	if err != nil {
		t.Fatalf("FetchPlatformStats: %v", err)
	}
	if stats.Replicas != 3 {
		t.Fatalf("replicas = %d, want 3", stats.Replicas)
	}
	if stats.RPS < 42.4 || stats.RPS > 42.6 {
		t.Fatalf("rps = %v, want ~42.5", stats.RPS)
	}
	if stats.P95Ms < 84.9 || stats.P95Ms > 85.1 {
		t.Fatalf("p95Ms = %v, want ~85", stats.P95Ms)
	}
	if stats.Source != "observe" {
		t.Fatalf("source = %q, want observe", stats.Source)
	}
}

func TestStatsUsesObserveValues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics/query", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		var value float64
		switch {
		case strings.Contains(q, "forge_replicas_ready"):
			value = 2
		case strings.Contains(q, "forge_http_requests_total"):
			value = 12
		default:
			value = 0.04
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":       q,
			"result_type": "vector",
			"samples":     []map[string]any{{"value": value}},
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	t.Setenv("HOSTNAME", "pulse-observe")
	t.Setenv("PULSEBOARD_REPLICAS", "1")
	observe := &ObserveClient{BaseURL: ts.URL, App: "pulseboard-api", HTTPClient: ts.Client()}
	srv := newServer(observe, nil, nil)
	handler := srv.routes()

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var stats Stats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Replicas != 2 {
		t.Fatalf("replicas = %d, want 2 (from Observe)", stats.Replicas)
	}
	if stats.RPS != 12 {
		t.Fatalf("rps = %v, want 12", stats.RPS)
	}
	if stats.P95Ms < 39.9 || stats.P95Ms > 40.1 {
		t.Fatalf("p95Ms = %v, want ~40", stats.P95Ms)
	}
	if stats.Source != "observe" {
		t.Fatalf("source = %q, want observe", stats.Source)
	}
}
