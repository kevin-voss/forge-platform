package dns

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"forge.local/services/forge-discovery/internal/store"
	mdns "github.com/miekg/dns"
)

func TestParseOwnerName(t *testing.T) {
	cases := []struct {
		qname string
		ok    bool
		kind  OwnerKind
		svc   string
		env   string
		proj  string
		port  string
		proto string
		epid  string
	}{
		{
			qname: "invoice-api.production.invoice-platform.svc.forge.",
			ok: true, kind: OwnerService, svc: "invoice-api", env: "production", proj: "invoice-platform",
		},
		{
			qname: "_http._tcp.invoice-api.production.invoice-platform.svc.forge",
			ok: true, kind: OwnerSRV, svc: "invoice-api", env: "production", proj: "invoice-platform",
			port: "http", proto: "tcp",
		},
		{
			qname: "ep-1.invoice-api.production.invoice-platform.svc.forge",
			ok: true, kind: OwnerEndpoint, svc: "invoice-api", env: "production", proj: "invoice-platform",
			epid: "ep-1",
		},
		{qname: "too.short.svc.forge", ok: false},
		{qname: "example.com", ok: false},
		{qname: "a.b.c.d.e.f.svc.forge", ok: false},
	}
	for _, tc := range cases {
		got, ok := ParseOwnerName(tc.qname, "svc.forge")
		if ok != tc.ok {
			t.Fatalf("%s: ok=%v want %v", tc.qname, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if got.Kind != tc.kind || got.Service != tc.svc || got.Environment != tc.env || got.Project != tc.proj {
			t.Fatalf("%s: got %+v", tc.qname, got)
		}
		if tc.kind == OwnerSRV && (got.PortName != tc.port || got.Protocol != tc.proto) {
			t.Fatalf("%s: srv fields %+v", tc.qname, got)
		}
		if tc.kind == OwnerEndpoint && got.EndpointID != tc.epid {
			t.Fatalf("%s: endpoint id %q", tc.qname, got.EndpointID)
		}
	}
}

type memStore struct {
	services  map[string]store.ServiceRow
	endpoints []store.EndpointRow
}

func (m *memStore) key(project, env, name string) string {
	return project + "/" + env + "/" + name
}

func (m *memStore) LookupServiceByNameOrAlias(_ context.Context, project, environment, nameOrAlias string) (store.ServiceRow, error) {
	for _, svc := range m.services {
		if svc.Project != project || svc.Environment != environment {
			continue
		}
		if svc.Name == nameOrAlias {
			return svc, nil
		}
		for _, a := range svc.Aliases {
			if a == nameOrAlias {
				return svc, nil
			}
		}
	}
	return store.ServiceRow{}, store.ErrNotFound
}

func (m *memStore) ListEndpoints(_ context.Context, f store.ListFilter) ([]store.EndpointRow, error) {
	var out []store.EndpointRow
	for _, ep := range m.endpoints {
		if ep.Project != f.Project || ep.Environment != f.Environment || ep.Service != f.Service {
			continue
		}
		if f.ReadyOnly && ep.Phase != "Ready" {
			continue
		}
		out = append(out, ep)
	}
	return out, nil
}

func (m *memStore) GetEndpoint(_ context.Context, project, environment, id string) (store.EndpointRow, error) {
	for _, ep := range m.endpoints {
		if ep.Project == project && ep.Environment == environment && ep.ID == id {
			return ep, nil
		}
	}
	return store.EndpointRow{}, store.ErrNotFound
}

func TestAliasResolvesSameAddresses(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	st := &memStore{
		services: map[string]store.ServiceRow{
			"p/e/invoice-api": {
				Project: "invoice-platform", Environment: "production", Name: "invoice-api",
				Aliases: []string{"legacy-invoice"},
			},
		},
		endpoints: []store.EndpointRow{
			{
				ID: "ep-0", Project: "invoice-platform", Environment: "production", Service: "invoice-api",
				AddressIP: "172.20.0.14", AddressPort: 8080, Protocol: "http", Phase: "Ready",
				ExpiresAt: now.Add(20 * time.Second),
			},
			{
				ID: "ep-1", Project: "invoice-platform", Environment: "production", Service: "invoice-api",
				AddressIP: "172.20.0.19", AddressPort: 8080, Protocol: "http", Phase: "Ready",
				ExpiresAt: now.Add(20 * time.Second),
			},
			{
				ID: "pending", Project: "invoice-platform", Environment: "production", Service: "invoice-api",
				AddressIP: "172.20.0.99", AddressPort: 8080, Protocol: "http", Phase: "Pending",
				ExpiresAt: now.Add(20 * time.Second),
			},
		},
	}
	// Fix service project/env keys used above — Lookup iterates values.
	st.services = map[string]store.ServiceRow{
		"x": {
			Project: "invoice-platform", Environment: "production", Name: "invoice-api",
			Aliases: []string{"legacy-invoice"},
		},
	}
	z := &ZoneResolver{
		Store: st,
		Zone:  "svc.forge",
		TTL:   TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second},
		Now:   func() time.Time { return now },
	}

	canon, hit := z.Resolve(context.Background(), mdns.Question{
		Name: "invoice-api.production.invoice-platform.svc.forge.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET,
	})
	if !hit || canon.Rcode != mdns.RcodeSuccess {
		t.Fatalf("canonical: hit=%v rcode=%d", hit, canon.Rcode)
	}
	alias, hit := z.Resolve(context.Background(), mdns.Question{
		Name: "legacy-invoice.production.invoice-platform.svc.forge.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET,
	})
	if !hit || alias.Rcode != mdns.RcodeSuccess {
		t.Fatalf("alias: hit=%v rcode=%d", hit, alias.Rcode)
	}
	if len(canon.Answer) != 2 || len(alias.Answer) != 2 {
		t.Fatalf("answer counts canon=%d alias=%d", len(canon.Answer), len(alias.Answer))
	}
	ips := func(msg *mdns.Msg) map[string]bool {
		out := map[string]bool{}
		for _, rr := range msg.Answer {
			if a, ok := rr.(*mdns.A); ok {
				out[a.A.String()] = true
			}
		}
		return out
	}
	cIPs, aIPs := ips(canon), ips(alias)
	for ip := range cIPs {
		if !aIPs[ip] {
			t.Fatalf("alias missing %s", ip)
		}
	}
	if cIPs["172.20.0.99"] {
		t.Fatal("Pending endpoint should not appear")
	}
}

func TestNXDOMAINNoReady(t *testing.T) {
	st := &memStore{
		services: map[string]store.ServiceRow{
			"x": {Project: "demo", Environment: "local", Name: "empty"},
		},
	}
	z := &ZoneResolver{
		Store: st,
		Zone:  "svc.forge",
		TTL:   TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second},
	}
	msg, hit := z.Resolve(context.Background(), mdns.Question{
		Name: "empty.local.demo.svc.forge.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET,
	})
	if !hit || msg.Rcode != mdns.RcodeNameError {
		t.Fatalf("want NXDOMAIN, got hit=%v rcode=%d", hit, msg.Rcode)
	}
	if len(msg.Ns) == 0 {
		t.Fatal("expected SOA for negative TTL")
	}
	soa, ok := msg.Ns[0].(*mdns.SOA)
	if !ok || soa.Minttl != 2 {
		t.Fatalf("SOA minttl = %#v", msg.Ns[0])
	}
}

func TestMalformedOwnerIsNXDOMAIN(t *testing.T) {
	z := &ZoneResolver{
		Store: &memStore{},
		Zone:  "svc.forge",
		TTL:   TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second},
	}
	msg, hit := z.Resolve(context.Background(), mdns.Question{
		Name: "not-enough-labels.svc.forge.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET,
	})
	if !hit {
		t.Fatal("expected zone hit")
	}
	if msg.Rcode != mdns.RcodeNameError {
		t.Fatalf("rcode=%d", msg.Rcode)
	}
}

func TestSRVRecords(t *testing.T) {
	now := time.Now().UTC()
	st := &memStore{
		services: map[string]store.ServiceRow{
			"x": {Project: "demo", Environment: "local", Name: "demo-echo"},
		},
		endpoints: []store.EndpointRow{{
			ID: "demo-echo-abc-0", Project: "demo", Environment: "local", Service: "demo-echo",
			AddressIP: "10.0.0.5", AddressPort: 8080, Protocol: "http", Phase: "Ready",
			ExpiresAt: now.Add(20 * time.Second),
		}},
	}
	z := &ZoneResolver{Store: st, Zone: "svc.forge", TTL: TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second}}
	msg, hit := z.Resolve(context.Background(), mdns.Question{
		Name: "_http._tcp.demo-echo.local.demo.svc.forge.", Qtype: mdns.TypeSRV, Qclass: mdns.ClassINET,
	})
	if !hit || msg.Rcode != mdns.RcodeSuccess || len(msg.Answer) != 1 {
		t.Fatalf("srv: %#v", msg)
	}
	srv := msg.Answer[0].(*mdns.SRV)
	if srv.Port != 8080 || srv.Target != "demo-echo-abc-0.demo-echo.local.demo.svc.forge." {
		t.Fatalf("srv = %+v", srv)
	}
	if len(msg.Extra) != 1 {
		t.Fatalf("glue = %d", len(msg.Extra))
	}
	if a := msg.Extra[0].(*mdns.A); !a.A.Equal(net.ParseIP("10.0.0.5").To4()) {
		t.Fatalf("glue A = %v", a.A)
	}
}

func TestParseDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()
	_, _ = ParseOwnerName("", "")
	_, _ = ParseOwnerName("....svc.forge", "svc.forge")
	_ = errors.New("ok")
}
