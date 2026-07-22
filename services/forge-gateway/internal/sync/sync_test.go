package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge.local/services/forge-gateway/internal/health"
	"forge.local/services/forge-gateway/internal/proxy"
	"forge.local/services/forge-gateway/internal/routes"
)

func TestApplyHostPattern(t *testing.T) {
	got := ApplyHostPattern("{service}.{project}.demo.localhost", "api", "acme")
	if got != "api.acme.demo.localhost" {
		t.Fatalf("got %q", got)
	}
	if ApplyHostPattern("{service}.{project}.demo.localhost", "", "acme") != "" {
		t.Fatal("expected empty host when service missing")
	}
}

func TestDeriveRoutesFromEndpoints(t *testing.T) {
	eps := []Endpoint{
		{
			Service:   "api",
			Project:   "acme",
			Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:49152"}},
			Ready:     BoolPtr(true),
		},
		{
			Host:      "custom.localhost",
			Service:   "ignored",
			Project:   "ignored",
			Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:5000"}},
		},
		// malformed: no upstreams → skipped
		{Service: "bad", Project: "acme"},
	}
	got := DeriveRoutes(eps, DefaultHostPattern)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	if got[0].Host != "api.acme.demo.localhost" {
		t.Fatalf("host[0]=%q", got[0].Host)
	}
	if got[0].Upstreams[0].URL != "http://127.0.0.1:49152" {
		t.Fatalf("upstream=%q", got[0].Upstreams[0].URL)
	}
	if got[1].Host != "custom.localhost" {
		t.Fatalf("host[1]=%q", got[1].Host)
	}
}

func TestDeriveRoutesAggregatesUpstreams(t *testing.T) {
	eps := []Endpoint{
		{Service: "api", Project: "acme", Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:1"}}},
		{Service: "api", Project: "acme", Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:2"}}},
	}
	got := DeriveRoutes(eps, DefaultHostPattern)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if len(got[0].Upstreams) != 2 {
		t.Fatalf("upstreams=%d want 2", len(got[0].Upstreams))
	}
}

func TestDiffHosts(t *testing.T) {
	before := []routes.Route{
		{Host: "a.demo.localhost", Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:1"}}, Strategy: routes.StrategyRoundRobin},
		{Host: "b.demo.localhost", Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:2"}}, Strategy: routes.StrategyRoundRobin},
	}
	after := []routes.Route{
		{Host: "b.demo.localhost", Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:2"}}, Strategy: routes.StrategyRoundRobin},
		{Host: "c.demo.localhost", Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:3"}}, Strategy: routes.StrategyRoundRobin},
	}
	added, removed := DiffHosts(before, after)
	if strings.Join(added, ",") != "c.demo.localhost" {
		t.Fatalf("added=%v", added)
	}
	if strings.Join(removed, ",") != "a.demo.localhost" {
		t.Fatalf("removed=%v", removed)
	}
}

type staticSource struct {
	eps []Endpoint
	err error
	n   string
}

func (s *staticSource) Name() string { return s.n }
func (s *staticSource) Fetch(context.Context) ([]Endpoint, error) {
	return s.eps, s.err
}

func TestSyncOnceLastGoodRetention(t *testing.T) {
	table := routes.NewTable()
	seed := []routes.Route{{
		Host:      "api.acme.demo.localhost",
		Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:9"}},
		Strategy:  routes.StrategyRoundRobin,
	}}
	if err := table.Replace(seed); err != nil {
		t.Fatal(err)
	}

	src := &staticSource{n: "test", err: errors.New("source down")}
	syncer := New(Config{
		Table:  table,
		Proxy:  proxy.NewHandler(table, slog.Default(), nil, proxy.Config{}),
		Source: src,
		Log:    slog.Default(),
	})
	result := syncer.SyncOnce(context.Background())
	if result.OK {
		t.Fatal("expected failure")
	}
	if result.RoutesLoaded != 1 {
		t.Fatalf("RoutesLoaded=%d want 1 (last-good)", result.RoutesLoaded)
	}
	snap := table.Snapshot()
	if len(snap) != 1 || snap[0].Host != "api.acme.demo.localhost" {
		t.Fatalf("table wiped: %+v", snap)
	}
}

func TestSyncOnceReplacesAndDiffs(t *testing.T) {
	table := routes.NewTable()
	_ = table.Replace([]routes.Route{{
		Host:      "old.demo.localhost",
		Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:1"}},
		Strategy:  routes.StrategyRoundRobin,
	}})

	src := &staticSource{
		n: "test",
		eps: []Endpoint{{
			Service:   "api",
			Project:   "acme",
			Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:49152"}},
		}},
	}
	syncer := New(Config{Table: table, Source: src, Log: slog.Default()})
	result := syncer.SyncOnce(context.Background())
	if !result.OK {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.RoutesLoaded != 1 {
		t.Fatalf("RoutesLoaded=%d", result.RoutesLoaded)
	}
	if strings.Join(result.Added, ",") != "api.acme.demo.localhost" {
		t.Fatalf("added=%v", result.Added)
	}
	if strings.Join(result.Removed, ",") != "old.demo.localhost" {
		t.Fatalf("removed=%v", result.Removed)
	}
}

func TestHandleRefresh(t *testing.T) {
	table := routes.NewTable()
	src := &staticSource{
		n: "test",
		eps: []Endpoint{{
			Service:   "api",
			Project:   "acme",
			Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:49152"}},
		}},
	}
	syncer := New(Config{Table: table, Source: src, Log: slog.Default()})
	mux := http.NewServeMux()
	syncer.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/routes/refresh", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var result Result
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.RoutesLoaded != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestControlEndpointsSourceContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/endpoints", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]Endpoint{{
			Host:      "api.acme.demo.localhost",
			Service:   "api",
			Project:   "acme",
			Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:49152"}},
			Ready:     BoolPtr(true),
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &ControlEndpointsSource{BaseURL: srv.URL, Client: srv.Client()}
	eps, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].Host != "api.acme.demo.localhost" {
		t.Fatalf("eps=%+v", eps)
	}
}

func TestRuntimeInterimSourceJoinsControlMetadata(t *testing.T) {
	depID := "11111111-1111-1111-1111-111111111111"
	projectID := "22222222-2222-2222-2222-222222222222"

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/node/state", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodeId": "node-1",
			"workloads": []map[string]any{{
				"deploymentId": depID,
				"status":       "ready",
				"hostPort":     49173,
				"image":        "demo:latest",
			}},
		})
	})
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": projectID, "name": "Acme", "slug": "acme",
		}})
	})
	mux.HandleFunc("GET /v1/projects/"+projectID, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expand") != "tree" {
			http.Error(w, "missing expand", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project": map[string]any{"id": projectID, "name": "Acme", "slug": "acme"},
			"applications": []map[string]any{{
				"services": []map[string]any{{
					"name": "api",
					"deployments": []map[string]any{{
						"id": depID,
					}},
				}},
			}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &RuntimeInterimSource{
		ControlURL:   srv.URL,
		RuntimeURL:   srv.URL,
		UpstreamHost: "host.docker.internal",
		Client:       srv.Client(),
	}
	eps, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 {
		t.Fatalf("len=%d", len(eps))
	}
	if eps[0].Service != "api" || eps[0].Project != "acme" {
		t.Fatalf("meta=%+v", eps[0])
	}
	if eps[0].Upstreams[0].URL != "http://host.docker.internal:49173" {
		t.Fatalf("upstream=%q", eps[0].Upstreams[0].URL)
	}
	if eps[0].Ready == nil || !*eps[0].Ready {
		t.Fatalf("Ready=%v, want true", eps[0].Ready)
	}

	routes := DeriveRoutes(eps, DefaultHostPattern)
	if len(routes) != 1 || routes[0].Host != "api.acme.demo.localhost" {
		t.Fatalf("routes=%+v", routes)
	}
}

func TestSyncOnceAppliesUpstreamReadiness(t *testing.T) {
	table := routes.NewTable()
	tracker := health.NewUpstreamTracker(health.UpstreamConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		TrustRuntimeStatus: true,
	}, slog.Default())
	src := &staticSource{
		n: "test",
		eps: []Endpoint{{
			Service:   "api",
			Project:   "acme",
			Upstreams: []UpstreamRef{{URL: "http://127.0.0.1:49152"}},
			Ready:     BoolPtr(false),
		}},
	}
	syncer := New(Config{
		Table:   table,
		Tracker: tracker,
		Source:  src,
		Log:     slog.Default(),
	})
	if r := syncer.SyncOnce(context.Background()); !r.OK {
		t.Fatalf("sync: %s", r.Error)
	}
	if tracker.IsReady("http://127.0.0.1:49152") {
		t.Fatal("expected sync Ready=false to mark upstream unready")
	}

	src.eps[0].Ready = BoolPtr(true)
	if r := syncer.SyncOnce(context.Background()); !r.OK {
		t.Fatalf("sync: %s", r.Error)
	}
	if !tracker.IsReady("http://127.0.0.1:49152") {
		t.Fatal("expected sync Ready=true to re-add upstream")
	}
}

func TestFallbackSourceUsesInterimOn404(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/endpoints", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("GET /v1/node/state", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"nodeId": "n", "workloads": []any{}})
	})
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src, err := BuildSource("control", srv.URL, srv.URL, "127.0.0.1", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	eps, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("control calls=%d", calls)
	}
	if eps == nil {
		t.Fatal("expected empty slice")
	}
}

func TestSyncOnceEmptySourceRemovesRoutes(t *testing.T) {
	table := routes.NewTable()
	_ = table.Replace([]routes.Route{{
		Host:      "api.acme.demo.localhost",
		Upstreams: []routes.Upstream{{URL: "http://127.0.0.1:1"}},
		Strategy:  routes.StrategyRoundRobin,
	}})
	src := &staticSource{n: "test", eps: []Endpoint{}}
	syncer := New(Config{Table: table, Source: src, Log: slog.Default()})
	result := syncer.SyncOnce(context.Background())
	if !result.OK || result.RoutesLoaded != 0 {
		t.Fatalf("result=%+v", result)
	}
	if table.Len() != 0 {
		t.Fatalf("expected empty table after successful empty sync")
	}
}

func TestProxyReachableAfterSync(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	table := routes.NewTable()
	src := &staticSource{
		n: "test",
		eps: []Endpoint{{
			Service:   "api",
			Project:   "acme",
			Upstreams: []UpstreamRef{{URL: upstream.URL}},
		}},
	}
	ph := proxy.NewHandler(table, slog.Default(), nil, proxy.Config{})
	syncer := New(Config{Table: table, Proxy: ph, Source: src, Log: slog.Default()})
	if r := syncer.SyncOnce(context.Background()); !r.OK {
		t.Fatalf("sync: %s", r.Error)
	}

	mux := http.NewServeMux()
	mux.Handle("/", ph)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "api.acme.demo.localhost"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Remove route on next sync → 404
	src.eps = nil
	_ = syncer.SyncOnce(context.Background())
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr2.Code)
	}
}
