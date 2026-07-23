package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"forge.local/services/forge-observe/internal/backends"
)

type stubReady struct {
	err error
}

func (s stubReady) ReadyError() error { return s.err }

type stubBackends struct {
	status map[string]string
}

func (s stubBackends) StatusMap(context.Context) map[string]string { return s.status }

func TestLiveAlwaysOK(t *testing.T) {
	h := NewHandler(stubReady{}, stubBackends{}, "forge-observe", "0.1.0")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestReadyReflectsChecker(t *testing.T) {
	h := NewHandler(stubReady{err: errors.New("down")}, stubBackends{}, "forge-observe", "0.1.0")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("not ready: status = %d, want 503", rr.Code)
	}

	h.ready = stubReady{}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ready: status = %d, want 200", rr.Code)
	}
}

func TestIdentityJSON(t *testing.T) {
	h := NewHandler(stubReady{}, stubBackends{}, "forge-observe", "0.1.0")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["service"] != "forge-observe" || body["language"] != "go" || body["status"] != "running" {
		t.Fatalf("identity = %#v", body)
	}
}

func TestBackendsEndpoint(t *testing.T) {
	h := NewHandler(stubReady{}, stubBackends{status: map[string]string{
		"loki": backends.StatusOK, "tempo": backends.StatusDown, "prometheus": backends.StatusOK,
	}}, "forge-observe", "0.1.0")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/health/backends", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["loki"] != "ok" || body["tempo"] != "down" || body["prometheus"] != "ok" {
		t.Fatalf("backends = %#v", body)
	}
}
