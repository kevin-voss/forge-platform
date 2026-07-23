package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryEndpointsSourceFetchShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"project": "invoice-platform", "environment": "demo",
			"name": "invoice-api", "aliases": []string{"legacy-invoice"},
		}})
	})
	mux.HandleFunc("GET /v1/projects/invoice-platform/environments/demo/services/invoice-api/endpoints", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "invoice-api-0", "phase": "Ready", "ready": true,
			"address": map[string]any{"ip": "172.20.0.14", "port": 8080},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &DiscoveryEndpointsSource{
		BaseURL:     srv.URL,
		HostPattern: DefaultHostPattern,
		Client:      srv.Client(),
	}
	eps, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("len=%d want 2 (canonical + alias): %+v", len(eps), eps)
	}
	if eps[0].Service != "invoice-api" || eps[0].Project != "invoice-platform" {
		t.Fatalf("canonical meta=%+v", eps[0])
	}
	if eps[0].Host != "" {
		t.Fatalf("canonical Host=%q want empty for DeriveRoutes", eps[0].Host)
	}
	if len(eps[0].Upstreams) != 1 || eps[0].Upstreams[0].URL != "http://172.20.0.14:8080" {
		t.Fatalf("upstream=%+v", eps[0].Upstreams)
	}
	if eps[0].Ready == nil || !*eps[0].Ready {
		t.Fatalf("Ready=%v", eps[0].Ready)
	}
	if eps[1].Host != "legacy-invoice.invoice-platform.demo.localhost" {
		t.Fatalf("alias host=%q", eps[1].Host)
	}
	if eps[1].Service != "invoice-api" || eps[1].Upstreams[0].URL != "http://172.20.0.14:8080" {
		t.Fatalf("alias endpoint=%+v", eps[1])
	}

	derived := DeriveRoutes(eps, DefaultHostPattern)
	if len(derived) != 2 {
		t.Fatalf("derived len=%d want 2: %+v", len(derived), derived)
	}
	hosts := map[string]bool{}
	for _, r := range derived {
		hosts[r.Host] = true
	}
	if !hosts["invoice-api.invoice-platform.demo.localhost"] || !hosts["legacy-invoice.invoice-platform.demo.localhost"] {
		t.Fatalf("hosts=%v", hosts)
	}
}

func TestDiscoveryAliasExpansionSharesUpstreams(t *testing.T) {
	ups := []UpstreamRef{{URL: "http://10.0.0.1:8080"}, {URL: "http://10.0.0.2:8080"}}
	got := expandAliasEndpoints("api", "acme", []string{"legacy", "api", ""}, ups, DefaultHostPattern, nil)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (skip empty + self-alias)", len(got))
	}
	if got[0].Host != "legacy.acme.demo.localhost" {
		t.Fatalf("host=%q", got[0].Host)
	}
	if len(got[0].Upstreams) != 2 {
		t.Fatalf("upstreams=%d", len(got[0].Upstreams))
	}
	if got[0].Service != "api" {
		t.Fatalf("service=%q want canonical", got[0].Service)
	}
}

func TestBuildSourceDiscoveryRequiresURL(t *testing.T) {
	_, err := BuildSource("discovery", "", "", "", "", DefaultHostPattern, nil)
	if err == nil {
		t.Fatal("expected error when FORGE_DISCOVERY_URL empty")
	}
	if !strings.Contains(err.Error(), "FORGE_DISCOVERY_URL") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildSourceDiscovery(t *testing.T) {
	src, err := BuildSource("discovery", "", "", "", "http://forge-discovery:8080", DefaultHostPattern, nil)
	if err != nil {
		t.Fatal(err)
	}
	if src.Name() != "discovery" {
		t.Fatalf("Name=%q", src.Name())
	}
	ds, ok := src.(*DiscoveryEndpointsSource)
	if !ok {
		t.Fatalf("type=%T", src)
	}
	if ds.BaseURL != "http://forge-discovery:8080" {
		t.Fatalf("BaseURL=%q", ds.BaseURL)
	}
}

func TestDiscoveryEndpointsSourceOmitsFailedService(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"project": "p", "environment": "e", "name": "good", "aliases": []string{}},
			{"project": "p", "environment": "e", "name": "bad", "aliases": []string{}},
		})
	})
	mux.HandleFunc("GET /v1/projects/p/environments/e/services/good/endpoints", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "g0", "phase": "Ready",
			"address": map[string]any{"ip": "10.0.0.1", "port": 8080},
		}})
	})
	mux.HandleFunc("GET /v1/projects/p/environments/e/services/bad/endpoints", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &DiscoveryEndpointsSource{BaseURL: srv.URL, Client: srv.Client()}
	eps, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].Service != "good" {
		t.Fatalf("eps=%+v", eps)
	}
}

func TestDiscoveryEndpointsSourceFailsWhenServiceListDown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := &DiscoveryEndpointsSource{BaseURL: srv.URL, Client: srv.Client()}
	_, err := src.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}
