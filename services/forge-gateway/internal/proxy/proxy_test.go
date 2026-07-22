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
	"time"

	"forge.local/services/forge-gateway/internal/admin"
	"forge.local/services/forge-gateway/internal/health"
	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/middleware"
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

	h := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{})
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

	h := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{})
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

	h := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{})
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
	h := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{})
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

func TestProxySkipsUnreadyAnd503WhenNoneReady(t *testing.T) {
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

	tracker := health.NewUpstreamTracker(health.UpstreamConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
	}, slog.Default())
	tracker.Reconcile([]string{upA.URL, upB.URL})
	tracker.RecordPassiveFailure(upA.URL)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host: "health.demo.localhost",
		Upstreams: []routes.Upstream{
			{URL: upA.URL},
			{URL: upB.URL},
		},
		Strategy: routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default(), tracker, proxy.Config{})
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "health.demo.localhost"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != "B" {
			t.Fatalf("status=%d body=%q, want 200 B", rr.Code, rr.Body.String())
		}
	}
	if hitsA.Load() != 0 || hitsB.Load() != 4 {
		t.Fatalf("hits A=%d B=%d, want all on B", hitsA.Load(), hitsB.Load())
	}

	tracker.RecordPassiveFailure(upB.URL)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "health.demo.localhost"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rr.Code)
	}
	var env httperr.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "no_healthy_upstream" {
		t.Fatalf("code=%q, want no_healthy_upstream", env.Error.Code)
	}
	if env.Error.RequestID == "" {
		t.Fatal("requestId required")
	}
}

func TestProxyPassiveMarkingFromConnectionErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	tracker := health.NewUpstreamTracker(health.UpstreamConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
	}, slog.Default())
	upstreamURL := "http://" + addr
	tracker.Reconcile([]string{upstreamURL})

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "passive.demo.localhost",
		Upstreams: []routes.Upstream{{URL: upstreamURL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default(), tracker, proxy.Config{})

	// First failure: still ready → 502
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "passive.demo.localhost"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("first status=%d, want 502", rr.Code)
	}
	if !tracker.IsReady(upstreamURL) {
		t.Fatal("should still be ready after one failure")
	}

	// Second failure trips threshold → subsequent request is 503
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("second status=%d, want 502", rr.Code)
	}
	if tracker.IsReady(upstreamURL) {
		t.Fatal("should be unready after threshold")
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("third status=%d, want 503", rr.Code)
	}
	var env httperr.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "no_healthy_upstream" {
		t.Fatalf("code=%q", env.Error.Code)
	}
}

func TestProxyPassiveMarkingFrom5xx(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "boom")
	}))
	t.Cleanup(up.Close)

	tracker := health.NewUpstreamTracker(health.UpstreamConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
	}, slog.Default())
	tracker.Reconcile([]string{up.URL})

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "fivexx.demo.localhost",
		Upstreams: []routes.Upstream{{URL: up.URL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default(), tracker, proxy.Config{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "fivexx.demo.localhost"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502 from upstream", rr.Code)
	}
	if tracker.IsReady(up.URL) {
		t.Fatal("5xx should passively mark unready")
	}
}

func TestProxyPropagatesRequestIDAndForwardedHeaders(t *testing.T) {
	var gotID, gotXFF, gotProto, gotHost, gotForwarded string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("X-Request-Id")
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotProto = r.Header.Get("X-Forwarded-Proto")
		gotHost = r.Header.Get("X-Forwarded-Host")
		gotForwarded = r.Header.Get("Forwarded")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "echo.demo.localhost",
		Upstreams: []routes.Upstream{{URL: upstream.URL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := middleware.RequestID("")(proxy.NewHandler(table, slog.Default(), nil, proxy.Config{}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "echo.demo.localhost"
	req.RemoteAddr = "203.0.113.9:4000"
	req.Header.Set("X-Request-Id", "client-req-1")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Keep-Alive", "timeout=5")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Request-Id"); got != "client-req-1" {
		t.Fatalf("response X-Request-Id=%q", got)
	}
	if gotID != "client-req-1" {
		t.Fatalf("upstream X-Request-Id=%q", gotID)
	}
	if gotXFF != "203.0.113.9" {
		t.Fatalf("upstream X-Forwarded-For=%q (trust disabled)", gotXFF)
	}
	if gotProto != "http" || gotHost != "echo.demo.localhost" {
		t.Fatalf("proto=%q host=%q", gotProto, gotHost)
	}
	if !strings.Contains(gotForwarded, "for=203.0.113.9") || !strings.Contains(gotForwarded, "proto=http") {
		t.Fatalf("Forwarded=%q", gotForwarded)
	}
}

func TestProxyOverallTimeoutReturns504(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = io.WriteString(w, "slow")
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:      "slow.demo.localhost",
		Upstreams: []routes.Upstream{{URL: upstream.URL}},
		Strategy:  routes.StrategyRoundRobin,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := middleware.RequestID("")(proxy.NewHandler(table, slog.Default(), nil, proxy.Config{
		OverallTimeout: 100 * time.Millisecond,
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "slow.demo.localhost"
	rr := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s, want 504", rr.Code, rr.Body.String())
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("elapsed=%v, expected timeout near 100ms", elapsed)
	}
	var env httperr.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "gateway_timeout" {
		t.Fatalf("code=%q", env.Error.Code)
	}
	if env.Error.RequestID == "" {
		t.Fatal("requestId required in 504 envelope")
	}
	if rr.Header().Get("X-Request-Id") == "" {
		t.Fatal("X-Request-Id required on 504 response")
	}
	if env.Error.RequestID != rr.Header().Get("X-Request-Id") {
		t.Fatalf("envelope requestId=%q header=%q", env.Error.RequestID, rr.Header().Get("X-Request-Id"))
	}
}

func TestProxyTimeoutExemptSkipsOverallDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, "stream")
	}))
	t.Cleanup(upstream.Close)

	table := routes.NewTable()
	if err := table.Replace([]routes.Route{{
		Host:          "stream.demo.localhost",
		Upstreams:     []routes.Upstream{{URL: upstream.URL}},
		Strategy:      routes.StrategyRoundRobin,
		TimeoutExempt: true,
	}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	h := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{
		OverallTimeout: 50 * time.Millisecond,
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "stream.demo.localhost"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "stream" {
		t.Fatalf("status=%d body=%q, want 200 stream (timeout exempt)", rr.Code, rr.Body.String())
	}
}
