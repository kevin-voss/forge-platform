package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthReady(t *testing.T) {
	srv := newServer(nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %q, want ok", body["status"])
	}
}

func TestStatsBaselineReplicas(t *testing.T) {
	t.Setenv("PULSEBOARD_REPLICAS", "1")
	t.Setenv("HOSTNAME", "pulse-unit")
	srv := newServer(nil, nil, nil)
	handler := srv.routes()

	hitReq := httptest.NewRequest(http.MethodPost, "/hit", nil)
	hitRec := httptest.NewRecorder()
	handler.ServeHTTP(hitRec, hitReq)
	if hitRec.Code != http.StatusOK {
		t.Fatalf("hit status = %d, want 200; body=%s", hitRec.Code, hitRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var stats Stats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Replicas != 1 {
		t.Fatalf("replicas = %d, want 1", stats.Replicas)
	}
	if stats.Counter < 2 {
		t.Fatalf("counter = %d, want >= 2 (hit + stats)", stats.Counter)
	}
	if stats.Instance != "pulse-unit" {
		t.Fatalf("instance = %q, want pulse-unit", stats.Instance)
	}
	if stats.UpdatedAt == "" {
		t.Fatal("updatedAt empty")
	}
	if stats.Source != "local" {
		t.Fatalf("source = %q, want local when Observe unset", stats.Source)
	}
}
