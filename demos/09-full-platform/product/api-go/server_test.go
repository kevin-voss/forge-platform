package main

import (
	"bytes"
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
		ServiceName:    "incident-api",
		ServiceVersion: "0.1.0",
		LogLevel:       "info",
		Env:            "development",
	}
	return newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestHealthEndpoints(t *testing.T) {
	srv := testServer(t)
	handler := srv.routes()

	for _, path := range []string{"/health/live", "/health/ready"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
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
	if body.Service != "incident-api" {
		t.Errorf("service = %q, want incident-api", body.Service)
	}
	if body.Language != "go" {
		t.Errorf("language = %q, want go", body.Language)
	}
	if body.Status != "running" {
		t.Errorf("status = %q, want running", body.Status)
	}
	if body.UptimeSeconds <= 0 {
		t.Errorf("uptime_seconds = %v, want > 0", body.UptimeSeconds)
	}
}

func TestCreateAndGetIncident(t *testing.T) {
	srv := testServer(t)
	handler := srv.routes()

	payload := []byte(`{"title":"API latency","description":"p99 spike","severity":"high"}`)
	req := httptest.NewRequest(http.MethodPost, "/incidents", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", rec.Code)
	}

	var created incident
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Title != "API latency" || created.Status != "open" {
		t.Fatalf("unexpected incident: %+v", created)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/incidents/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", getRec.Code)
	}
}

func TestLoadConfigRequiresPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("FORGE_LOG_LEVEL", "info")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for missing PORT")
	}
}
