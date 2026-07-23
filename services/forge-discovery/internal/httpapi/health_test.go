package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubDB struct {
	err error
}

func (s stubDB) Ready(context.Context) error { return s.err }

func TestLiveAlwaysOK(t *testing.T) {
	ready := NewReadiness(stubDB{})
	mux := NewRouter(ready)
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestReadyRequiresDBAndKinds(t *testing.T) {
	db := &stubDB{}
	ready := NewReadiness(db)
	mux := NewRouter(ready)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("before kinds: %d", rr.Code)
	}

	ready.MarkKindsRegistered()
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("after kinds: %d", rr.Code)
	}

	db.err = errors.New("down")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("db down: %d", rr.Code)
	}
}

func TestReadyRequiresDNSWhenAttached(t *testing.T) {
	db := &stubDB{}
	dns := &stubDB{}
	ready := NewReadiness(db)
	ready.SetDNS(dns)
	ready.MarkKindsRegistered()
	mux := NewRouter(ready)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dns ok: %d", rr.Code)
	}

	dns.err = errors.New("dns down")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("dns down: %d", rr.Code)
	}
}
