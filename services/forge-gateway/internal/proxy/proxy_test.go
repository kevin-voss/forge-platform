package proxy_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"forge.local/services/forge-gateway/internal/admin"
	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/proxy"
	"forge.local/services/forge-gateway/internal/routes"
)

func TestProxyMatchAndNoMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "ok")
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "go.demo.localhost",
		Upstreams: []routes.Upstream{{URL: upstream.URL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default())
	mux := http.NewServeMux()
	admin.NewRoutesHandler(table, h, slog.Default()).Register(mux)
	mux.Handle("/", h)

	req := httptest.NewRequest(http.MethodGet, "http://gateway/", nil)
	req.Host = "go.demo.localhost"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "hello" {
		t.Fatalf("body=%q", rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "http://gateway/", nil)
	req.Host = "nope.localhost"
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rr.Code)
	}
	var env httperr.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "no_route" {
		t.Fatalf("code=%q, want no_route", env.Error.Code)
	}
	if env.Error.RequestID == "" {
		t.Fatal("requestId required")
	}
}

func TestProxyRoundRobinAlternates(t *testing.T) {
	var hitsA, hitsB atomic.Int64
	upA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitsA.Add(1)
		_, _ = io.WriteString(w, "A")
	}))
	t.Cleanup(upA.Close)
	upB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitsB.Add(1)
		_, _ = io.WriteString(w, "B")
	}))
	t.Cleanup(upB.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host: "rr.demo.localhost",
		Upstreams: []routes.Upstream{
			{URL: upA.URL},
			{URL: upB.URL},
		},
		Strategy: routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default())
	var bodies string
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "rr.demo.localhost"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		bodies += rr.Body.String()
	}
	if bodies != "ABAB" && bodies != "BABA" {
		t.Fatalf("bodies=%q, want alternating ABAB or BABA", bodies)
	}
	if hitsA.Load() != 2 || hitsB.Load() != 2 {
		t.Fatalf("hits A=%d B=%d", hitsA.Load(), hitsB.Load())
	}
}

func TestProxyUpstreamError502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // closed listener → connection refused

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "bad.demo.localhost",
		Upstreams: []routes.Upstream{{URL: "http://" + addr}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "bad.demo.localhost"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", rr.Code)
	}
	var env httperr.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "bad_gateway" {
		t.Fatalf("code=%q", env.Error.Code)
	}
}

func TestAdminPutGetAndContractShapes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	h := proxy.NewHandler(table, slog.Default())
	mux := http.NewServeMux()
	admin.NewRoutesHandler(table, h, slog.Default()).Register(mux)
	mux.Handle("/", h)

	body := `[{"host":"go.demo.localhost","pathPrefix":"/","upstreams":[{"url":"` + upstream.URL + `"}],"strategy":"round_robin"}]`
	req := httptest.NewRequest(http.MethodPut, "/admin/routes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}

	var snap []routes.Route
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	if len(snap) != 1 || snap[0].Host != "go.demo.localhost" || len(snap[0].Upstreams) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap[0].Strategy != routes.StrategyRoundRobin {
		t.Fatalf("strategy=%q", snap[0].Strategy)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/routes", nil)
	getRR := httptest.NewRecorder()
	mux.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET status=%d", getRR.Code)
	}

	// Invalid PUT body → 400
	bad := httptest.NewRequest(http.MethodPut, "/admin/routes", strings.NewReader(`{"not":"array"}`))
	badRR := httptest.NewRecorder()
	mux.ServeHTTP(badRR, bad)
	if badRR.Code != http.StatusBadRequest {
		t.Fatalf("bad PUT status=%d", badRR.Code)
	}

	// Proxied after PUT
	proxyReq := httptest.NewRequest(http.MethodGet, "/", nil)
	proxyReq.Host = "go.demo.localhost"
	proxyRR := httptest.NewRecorder()
	mux.ServeHTTP(proxyRR, proxyReq)
	if proxyRR.Code != http.StatusOK || proxyRR.Body.String() != "ok" {
		t.Fatalf("proxy after PUT: status=%d body=%q", proxyRR.Code, proxyRR.Body.String())
	}
}
