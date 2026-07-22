package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge.local/services/forge-gateway/internal/middleware"
)

func TestApplyForwardedWithoutTrust(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://go.demo.localhost/path", nil)
	req.Host = "go.demo.localhost"
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	middleware.ApplyForwarded(req, middleware.ForwardedOptions{TrustInboundXFF: false})

	if got := req.Header.Get("X-Forwarded-For"); got != "203.0.113.10" {
		t.Fatalf("X-Forwarded-For=%q, want observed client only", got)
	}
	if got := req.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Fatalf("X-Forwarded-Proto=%q", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "go.demo.localhost" {
		t.Fatalf("X-Forwarded-Host=%q", got)
	}
	fwd := req.Header.Get("Forwarded")
	if !strings.Contains(fwd, `for=203.0.113.10`) || !strings.Contains(fwd, "proto=http") || !strings.Contains(fwd, "host=go.demo.localhost") {
		t.Fatalf("Forwarded=%q", fwd)
	}
}

func TestApplyForwardedWithTrustAppends(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "api.example"
	req.RemoteAddr = "198.51.100.7:9"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	middleware.ApplyForwarded(req, middleware.ForwardedOptions{TrustInboundXFF: true})

	if got := req.Header.Get("X-Forwarded-For"); got != "1.2.3.4, 5.6.7.8, 198.51.100.7" {
		t.Fatalf("X-Forwarded-For=%q", got)
	}
}

func TestStripHopByHop(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "keep-alive, X-Custom-Hop")
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Proxy-Authorization", "Basic abc")
	h.Set("Upgrade", "websocket")
	h.Set("X-Custom-Hop", "1")
	h.Set("X-Request-Id", "keep-me")
	h.Set("Content-Type", "application/json")

	middleware.StripHopByHop(h)

	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authorization", "Upgrade", "X-Custom-Hop"} {
		if h.Get(name) != "" {
			t.Fatalf("expected %s stripped, got %q", name, h.Get(name))
		}
	}
	if h.Get("X-Request-Id") != "keep-me" {
		t.Fatal("end-to-end headers must be preserved")
	}
	if h.Get("Content-Type") != "application/json" {
		t.Fatal("Content-Type must be preserved")
	}
}
