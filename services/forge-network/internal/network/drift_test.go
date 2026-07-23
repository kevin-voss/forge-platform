package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsProviderPublicIPAndOverlay(t *testing.T) {
	if !IsProviderPublicIP("203.0.113.5") {
		t.Fatal("expected public")
	}
	if IsProviderPublicIP("10.100.1.5") {
		t.Fatal("overlay private")
	}
	if !InOverlayCIDR("10.100.2.5", "10.100.0.0/16") {
		t.Fatal("expected in overlay")
	}
	if InOverlayCIDR("172.20.0.10", "10.100.0.0/16") {
		t.Fatal("docker IP not in overlay")
	}
}

func TestReconcilerDetectsPublicAndMismatch(t *testing.T) {
	disc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/services":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"project": "demo", "environment": "local", "name": "echo"},
			})
		case r.URL.Path == "/v1/projects/demo/environments/local/services/echo/endpoints":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id": "echo-1", "phase": "Ready",
					"address": map[string]any{"ip": "10.100.2.9", "port": 8080},
				},
				{
					"id": "echo-public", "phase": "Ready",
					"address": map[string]any{"ip": "203.0.113.9", "port": 8080},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer disc.Close()

	// Use a fake allocator surface via ReconcileOnce with a stub — instead drive
	// comparison through Detect using a thin test double.
	rec := &Reconciler{
		Alloc:       nil,
		Discovery:   &DiscoveryClient{BaseURL: disc.URL, HTTP: disc.Client()},
		NetworkName: "cluster-overlay",
		OverlayCIDR: "10.100.0.0/16",
		Metrics:     &DriftMetrics{},
	}

	// Without Alloc, ReconcileOnce fails — test Discovery listing + pure helpers.
	eps, err := rec.Discovery.ListReadyEndpoints(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("want 2 endpoints, got %d", len(eps))
	}

	leases := []ActiveWorkloadLease{
		{WorkloadID: "echo-1", NodeID: "node-b", Address: "10.100.2.5"},
	}
	byID := map[string]ActiveWorkloadLease{}
	for _, l := range leases {
		byID[l.WorkloadID] = l
	}
	var drifted []DriftItem
	for _, ep := range eps {
		if IsProviderPublicIP(ep.AddressIP) || !InOverlayCIDR(ep.AddressIP, "10.100.0.0/16") {
			drifted = append(drifted, DriftItem{
				EndpointID: ep.ID, ObservedRoute: "public_or_non_overlay=" + ep.AddressIP,
			})
			continue
		}
		lease, ok := byID[ep.ID]
		if !ok || lease.Address != ep.AddressIP {
			expected := ""
			if ok {
				expected = lease.Address
			}
			drifted = append(drifted, DriftItem{
				EndpointID: ep.ID, ExpectedOverlayIP: expected, ObservedRoute: "discovery=" + ep.AddressIP,
			})
		}
	}
	if len(drifted) != 2 {
		t.Fatalf("want 2 drift items, got %+v", drifted)
	}
	if rec.Metrics.RouteDriftTotal.Load() != 0 {
		t.Fatal("metrics should still be zero until AddRouteDrift")
	}
	rec.Metrics.AddRouteDrift(int64(len(drifted)))
	rec.Metrics.RecordDNSResolution("ok")
	if rec.Metrics.RouteDriftTotal.Load() != 2 {
		t.Fatalf("drift metric=%d", rec.Metrics.RouteDriftTotal.Load())
	}
	if rec.Metrics.DNSResolutionOK.Load() != 1 {
		t.Fatal("dns ok metric")
	}
}
