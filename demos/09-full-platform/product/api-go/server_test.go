package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
		ProductAuth:    "dev",
	}
	store := newMemoryStore()
	h := &otelHandle{tracer: noopTracer()}
	return newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), store, h)
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
	if body.DBBackend != "memory" {
		t.Errorf("db_backend = %q, want memory", body.DBBackend)
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

func TestAuthEnforceRejectsMissingBearer(t *testing.T) {
	srv := testServer(t)
	srv.cfg.ProductAuth = "enforce"
	srv.identity = newIdentityClient("http://127.0.0.1:9", "")
	handler := srv.routes()

	req := httptest.NewRequest(http.MethodGet, "/incidents", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSecretStatusNeverLeaks(t *testing.T) {
	srv := testServer(t)
	srv.cfg.AppSharedSecret = "super-secret-value-xyz"
	srv.cfg.ProductMode = "capstone"
	handler := srv.routes()

	req := httptest.NewRequest(http.MethodGet, "/secret-status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "super-secret-value-xyz") {
		t.Fatalf("secret leaked in response: %s", body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["APP_SHARED_SECRET_present"] != true {
		t.Fatalf("expected present=true, got %#v", payload)
	}
}

func TestDBStatusNeverLeaks(t *testing.T) {
	srv := testServer(t)
	srv.cfg.DatabaseURL = "postgresql://user:hunter2@db:5432/incidents"
	handler := srv.routes()

	req := httptest.NewRequest(http.MethodGet, "/db-status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "postgresql://") {
		t.Fatalf("DATABASE_URL leaked: %s", body)
	}
}

func TestMaskLogLine(t *testing.T) {
	line := `{"msg":"url=postgresql://u:p@h/db secret=abc"}`
	masked := MaskLogLine(line, []string{"postgresql://u:p@h/db", "abc"})
	if strings.Contains(masked, "postgresql://u:p@h/db") || strings.Contains(masked, "abc") {
		t.Fatalf("not masked: %s", masked)
	}
	if !strings.Contains(masked, "***") {
		t.Fatalf("expected *** in %s", masked)
	}
}

func TestLoadConfigRequiresPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("FORGE_LOG_LEVEL", "info")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for missing PORT")
	}
}

func TestLoadConfigRejectsControlDB(t *testing.T) {
	t.Setenv("PORT", "8080")
	t.Setenv("DATABASE_URL", "postgresql://forge:forge@postgres:5432/forge")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error for Control DB URL")
	}
}
