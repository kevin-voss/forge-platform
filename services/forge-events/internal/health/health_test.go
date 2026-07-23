package health

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubReady struct {
	err error
}

func (s stubReady) ReadyError() error { return s.err }

func TestLiveAlwaysOK(t *testing.T) {
	h := NewHandler(stubReady{}, "forge-events", "0.1.0")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %q, want ok", body["status"])
	}
}

func TestReadyReflectsChecker(t *testing.T) {
	h := NewHandler(stubReady{err: errors.New("down")}, "forge-events", "0.1.0")
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
	h := NewHandler(stubReady{}, "forge-events", "0.1.0")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body identityResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Service != "forge-events" {
		t.Fatalf("service = %q, want forge-events", body.Service)
	}
	if body.Language != "go" {
		t.Fatalf("language = %q, want go", body.Language)
	}
	if body.Status != "running" {
		t.Fatalf("status = %q, want running", body.Status)
	}
}
