package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge.local/services/forge-discovery/internal/middleware"
)

func TestRequestIDGeneratesAndEchoes(t *testing.T) {
	var seen string
	h := middleware.RequestID("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Request-Id")
		if seen == "" {
			t.Fatal("request missing X-Request-Id")
		}
		if got := middleware.RequestIDFromContext(r.Context()); got != seen {
			t.Fatalf("context id=%q, header=%q", got, seen)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	echoed := rr.Header().Get("X-Request-Id")
	if echoed == "" {
		t.Fatal("response missing X-Request-Id")
	}
	if echoed != seen {
		t.Fatalf("echoed=%q seen=%q", echoed, seen)
	}
	if !strings.HasPrefix(echoed, "req_") {
		t.Fatalf("generated id should use req_ prefix, got %q", echoed)
	}
}

func TestRequestIDReusesIncoming(t *testing.T) {
	const want = "test-123"
	h := middleware.RequestID("X-Request-Id")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Request-Id"); got != want {
			t.Fatalf("upstream header=%q, want %q", got, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", want)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Request-Id"); got != want {
		t.Fatalf("echoed=%q, want %q", got, want)
	}
}

func TestRequestIDRejectsInvalidIncoming(t *testing.T) {
	h := middleware.RequestID("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "bad id with spaces" || id == "" {
			t.Fatalf("should regenerate invalid id, got %q", id)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "bad id with spaces")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("X-Request-Id") == "bad id with spaces" {
		t.Fatal("invalid incoming id must not be echoed")
	}
}
