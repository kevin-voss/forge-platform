package dns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"forge.local/services/forge-discovery/internal/store"
	mdns "github.com/miekg/dns"
)

func TestPublicIPRejectedFromDNS(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	st := &memStore{
		services: map[string]store.ServiceRow{
			"demo/local/echo": {Project: "demo", Environment: "local", Name: "echo"},
		},
		endpoints: []store.EndpointRow{
			{
				ID: "echo-public", Project: "demo", Environment: "local", Service: "echo",
				AddressIP: "203.0.113.10", AddressPort: 8080, Phase: "Ready", Ready: true,
				ExpiresAt: now.Add(20 * time.Second),
			},
			{
				ID: "echo-overlay", Project: "demo", Environment: "local", Service: "echo",
				AddressIP: "10.100.2.5", AddressPort: 8080, Phase: "Ready", Ready: true,
				ExpiresAt: now.Add(20 * time.Second),
			},
		},
	}
	cidr, err := ParseOverlayCIDR("10.100.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	z := &ZoneResolver{
		Store: st,
		Zone:  "svc.forge",
		TTL:   TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second},
		Now:   func() time.Time { return now },
		Overlay: &CIDROverlayFilter{OverlayCIDR: cidr},
	}
	msg, hit := z.Resolve(context.Background(), mdns.Question{
		Name: "echo.local.demo.svc.forge.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET,
	})
	if !hit {
		t.Fatal("expected zone hit")
	}
	if len(msg.Answer) != 1 {
		t.Fatalf("want 1 answer (overlay only), got %d", len(msg.Answer))
	}
	a, ok := msg.Answer[0].(*mdns.A)
	if !ok || a.A.String() != "10.100.2.5" {
		t.Fatalf("unexpected answer %#v", msg.Answer[0])
	}
}

func TestLeaseCheckerExcludesMissingLease(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	st := &memStore{
		services: map[string]store.ServiceRow{
			"demo/local/echo": {Project: "demo", Environment: "local", Name: "echo"},
		},
		endpoints: []store.EndpointRow{
			{
				ID: "echo-1", Project: "demo", Environment: "local", Service: "echo",
				AddressIP: "10.100.2.5", AddressPort: 8080, Phase: "Ready", Ready: true,
				ExpiresAt: now.Add(20 * time.Second),
			},
		},
	}
	cidr, _ := ParseOverlayCIDR("10.100.0.0/16")
	checker := &staticLeaseChecker{addrs: map[string]struct{}{}} // empty → no lease
	z := &ZoneResolver{
		Store: st, Zone: "svc.forge",
		TTL: TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second},
		Now: func() time.Time { return now },
		Overlay: &CIDROverlayFilter{OverlayCIDR: cidr, LeaseChecker: checker},
	}
	msg, _ := z.Resolve(context.Background(), mdns.Question{
		Name: "echo.local.demo.svc.forge.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET,
	})
	if msg.Rcode != mdns.RcodeNameError {
		t.Fatalf("want NXDOMAIN without lease, got rcode=%d answers=%d", msg.Rcode, len(msg.Answer))
	}
}

type staticLeaseChecker struct {
	addrs map[string]struct{}
}

func (s *staticLeaseChecker) HasLease(_ context.Context, _, addressIP string) bool {
	_, ok := s.addrs[addressIP]
	return ok
}

func TestNetworkLeaseIndexRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/networks/cluster-overlay/workload-leases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"leases":[{"workload_id":"echo-1","node_id":"node-b","address":"10.100.2.5"}]}`))
	}))
	defer srv.Close()

	idx := &NetworkLeaseIndex{BaseURL: srv.URL, NetworkName: "cluster-overlay", HTTP: srv.Client()}
	if err := idx.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !idx.HasLease(context.Background(), "echo-1", "10.100.2.5") {
		t.Fatal("expected lease present")
	}
	if idx.HasLease(context.Background(), "missing", "10.100.9.9") {
		t.Fatal("unexpected lease")
	}
}

func TestIsProviderPublicIP(t *testing.T) {
	if !IsProviderPublicIP("203.0.113.1") {
		t.Fatal("expected public")
	}
	if IsProviderPublicIP("10.100.1.1") {
		t.Fatal("overlay should not be public")
	}
	if IsProviderPublicIP("172.20.0.10") {
		t.Fatal("docker private should not be public")
	}
}
