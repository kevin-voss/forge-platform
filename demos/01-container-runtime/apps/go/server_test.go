package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testServer(t *testing.T) *server {
	t.Helper()
	cfg := config{
		Port:           8080,
		ServiceName:    "demo-go-api",
		ServiceVersion: "0.1.0",
		LogLevel:       "info",
		Env:            "development",
	}
	return newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHealthEndpoints(t *testing.T) {
	srv := testServer(t)
	handler := srv.routes()

	tests := []struct {
		name string
		path string
	}{
		{name: "live", path: "/health/live"},
		{name: "ready", path: "/health/ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}

			var body healthResponse
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Status != "ok" {
				t.Fatalf("status field = %q, want ok", body.Status)
			}
		})
	}
}

func TestIdentityEndpoint(t *testing.T) {
	srv := testServer(t)
	// Freeze uptime > 0
	srv.startedAt = time.Now().UTC().Add(-2 * time.Second)
	handler := srv.routes()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body identityResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Service != "demo-go-api" {
		t.Errorf("service = %q, want demo-go-api", body.Service)
	}
	if body.Language != "go" {
		t.Errorf("language = %q, want go", body.Language)
	}
	if body.Status != "running" {
		t.Errorf("status = %q, want running", body.Status)
	}
	if body.Version != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", body.Version)
	}
	if body.UptimeSeconds <= 0 {
		t.Errorf("uptime_seconds = %v, want > 0", body.UptimeSeconds)
	}
}

func TestLoadConfigRequiresPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("FORGE_LOG_LEVEL", "info")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for missing PORT")
	}
}

func TestLoadConfigRejectsInvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-port")
	t.Setenv("FORGE_LOG_LEVEL", "info")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for invalid PORT")
	}
}

func TestLoadConfigRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("FORGE_LOG_LEVEL", "verbose")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for invalid FORGE_LOG_LEVEL")
	}
}
