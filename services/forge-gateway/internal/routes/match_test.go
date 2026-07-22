package routes

import "testing"

func TestMatchHostWinsOverPathOnly(t *testing.T) {
	table := []Route{
		{Host: "", PathPrefix: "/api", Upstreams: []Upstream{{URL: "http://path.only"}}, Strategy: StrategyRoundRobin},
		{Host: "app.demo.localhost", PathPrefix: "/", Upstreams: []Upstream{{URL: "http://host.specific"}}, Strategy: StrategyRoundRobin},
	}
	got, ok := Match(table, "app.demo.localhost", "/api/v1")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Upstreams[0].URL != "http://host.specific" {
		t.Fatalf("host-specific should win, got %+v", got)
	}
}

func TestMatchLongestPathPrefix(t *testing.T) {
	table := []Route{
		{Host: "api.demo.localhost", PathPrefix: "/api", Upstreams: []Upstream{{URL: "http://short"}}, Strategy: StrategyRoundRobin},
		{Host: "api.demo.localhost", PathPrefix: "/api/v2", Upstreams: []Upstream{{URL: "http://long"}}, Strategy: StrategyRoundRobin},
	}
	got, ok := Match(table, "api.demo.localhost", "/api/v2/users")
	if !ok {
		t.Fatal("expected match")
	}
	if got.Upstreams[0].URL != "http://long" {
		t.Fatalf("longest prefix should win, got %+v", got)
	}
}

func TestMatchPathBoundary(t *testing.T) {
	table := []Route{
		{PathPrefix: "/api", Upstreams: []Upstream{{URL: "http://api"}}, Strategy: StrategyRoundRobin},
	}
	if _, ok := Match(table, "x.localhost", "/apiv2"); ok {
		t.Fatal("'/api' must not match '/apiv2'")
	}
	got, ok := Match(table, "x.localhost", "/api/v2")
	if !ok || got.Upstreams[0].URL != "http://api" {
		t.Fatalf("expected /api match, got ok=%v route=%+v", ok, got)
	}
}

func TestMatchNoMatch(t *testing.T) {
	table := []Route{
		{Host: "go.demo.localhost", PathPrefix: "/v1", Upstreams: []Upstream{{URL: "http://u"}}, Strategy: StrategyRoundRobin},
	}
	if _, ok := Match(table, "nope.localhost", "/"); ok {
		t.Fatal("expected no match for wrong host")
	}
	if _, ok := Match(table, "go.demo.localhost", "/other"); ok {
		t.Fatal("expected no match for wrong path")
	}
}

func TestMatchStripsHostPort(t *testing.T) {
	table := []Route{
		{Host: "go.demo.localhost", Upstreams: []Upstream{{URL: "http://u"}}, Strategy: StrategyRoundRobin},
	}
	got, ok := Match(table, "go.demo.localhost:4000", "/")
	if !ok || got.Host != "go.demo.localhost" {
		t.Fatalf("expected host match with port stripped, got ok=%v route=%+v", ok, got)
	}
}

func TestMatchHostCaseInsensitive(t *testing.T) {
	table := []Route{
		{Host: "Go.Demo.Localhost", Upstreams: []Upstream{{URL: "http://u"}}, Strategy: StrategyRoundRobin},
	}
	// Match normalizes via caller Normalized() in table; raw Match compares lowercased route host only if pre-normalized.
	normalized := []Route{table[0].Normalized()}
	got, ok := Match(normalized, "GO.DEMO.LOCALHOST", "/")
	if !ok {
		t.Fatal("expected case-insensitive host match")
	}
	if got.Upstreams[0].URL != "http://u" {
		t.Fatalf("unexpected route: %+v", got)
	}
}
