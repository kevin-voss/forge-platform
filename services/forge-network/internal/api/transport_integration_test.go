package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-network/internal/api"
	"forge.local/services/forge-network/internal/db"
	"forge.local/services/forge-network/internal/network"
)

func TestProviderPrivateThenWireguardOnMembershipChange(t *testing.T) {
	srv, name, cleanup := startTransportTestServer(t)
	defer cleanup()

	mustLease(t, srv, name, "node-a")
	mustLease(t, srv, name, "node-b")

	mustPatchMembership(t, srv, "node-a", `{"membership":"hetzner-private-fsn1"}`)
	mustPatchMembership(t, srv, "node-b", `{"membership":"hetzner-private-fsn1"}`)

	pair := mustGetTransport(t, srv, name, "node-a", "node-b")
	if pair.Transport != network.TransportProviderPrivate {
		t.Fatalf("transport=%s want provider-private", pair.Transport)
	}

	mustPatchMembership(t, srv, "node-b", `{"membership":"aws-vpc-use1"}`)
	pair = mustGetTransport(t, srv, name, "node-a", "node-b")
	if pair.Transport != network.TransportWireguard {
		t.Fatalf("after membership change transport=%s want wireguard", pair.Transport)
	}
}

func TestDockerColocatedComposeDemoNodes(t *testing.T) {
	srv, name, cleanup := startTransportTestServer(t)
	defer cleanup()

	mustLease(t, srv, name, "node-a")
	mustLease(t, srv, name, "node-b")
	mustLease(t, srv, name, "node-c")

	mustPatchMembership(t, srv, "node-a", `{"docker_colocated":true,"membership":"hetzner-private-fsn1"}`)
	mustPatchMembership(t, srv, "node-b", `{"docker_colocated":true,"membership":"hetzner-private-fsn1"}`)
	mustPatchMembership(t, srv, "node-c", `{"docker_colocated":true}`)

	for _, to := range []string{"node-b", "node-c"} {
		pair := mustGetTransport(t, srv, name, "node-a", to)
		if pair.Transport != network.TransportDocker {
			t.Fatalf("a→%s transport=%s want docker", to, pair.Transport)
		}
	}

	// Peers are still distributed for CIDR/route application; Runtime skips WG
	// when transport resolves to docker (asserted in Runtime route unit tests).
	mustRegister(t, srv, name, "node-a", "b64:a", "1.1.1.1:51820")
	mustRegister(t, srv, name, "node-b", "b64:b", "1.1.1.2:51820")
	ps := mustGetPeers(t, srv, name, "node-a")
	if len(ps.Peers) == 0 {
		t.Fatal("expected peer distribution to include node-b for route CIDRs")
	}
}

func TestMixedClusterProviderPrivateAndWireguard(t *testing.T) {
	srv, name, cleanup := startTransportTestServer(t)
	defer cleanup()

	mustLease(t, srv, name, "node-a")
	mustLease(t, srv, name, "node-b")
	mustLease(t, srv, name, "node-c")

	mustPatchMembership(t, srv, "node-a", `{"membership":"hetzner-private-fsn1"}`)
	mustPatchMembership(t, srv, "node-b", `{"membership":"hetzner-private-fsn1"}`)
	// node-c: no membership

	ab := mustGetTransport(t, srv, name, "node-a", "node-b")
	ac := mustGetTransport(t, srv, name, "node-a", "node-c")
	if ab.Transport != network.TransportProviderPrivate {
		t.Fatalf("a→b = %s", ab.Transport)
	}
	if ac.Transport != network.TransportWireguard {
		t.Fatalf("a→c = %s", ac.Transport)
	}
}

func startTransportTestServer(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	database, err := db.Open(ctx, dsn, "network", 4, true)
	cancel()
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}

	alloc := &network.Allocator{Pool: database.Pool, SkipDocker: true}
	reg := &network.PeerRegistry{Pool: database.Pool, KeepaliveSeconds: 25, MTU: 1420, Metrics: &network.PeerMetrics{}}
	mem := &network.MembershipStore{Pool: database.Pool, DefaultMode: network.TransportWireguard, Metrics: &network.TransportMetrics{}}
	comp := &network.PeerSetComputer{Registry: reg, Membership: mem}
	mux := api.NewRouter(api.Deps{Alloc: alloc, Registry: reg, Computer: comp, Membership: mem, DB: database})
	srv := httptest.NewServer(mux)

	ctx2 := context.Background()
	name := "itest-transport-" + time.Now().UTC().Format("150405.000")
	row, err := alloc.CreateNetwork(ctx2, name, "10.140.0.0/16", 24, nil)
	if err != nil {
		srv.Close()
		database.Close()
		t.Fatalf("create network: %v", err)
	}

	cleanup := func() {
		srv.Close()
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.network_routes WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.wireguard_peers WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.node_leases WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.networks WHERE id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.nodes WHERE node_id LIKE 'node-%'`)
		database.Close()
	}
	return srv, name, cleanup
}

func mustPatchMembership(t *testing.T, srv *httptest.Server, node, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/v1/nodes/"+node+"/network-membership", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("membership %s: %d %s", node, resp.StatusCode, b)
	}
}

func mustGetTransport(t *testing.T, srv *httptest.Server, name, from, to string) network.TransportPair {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/networks/" + name + "/transport?from=" + from + "&to=" + to)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("transport %s→%s: %d %s", from, to, resp.StatusCode, b)
	}
	var pair network.TransportPair
	if err := json.NewDecoder(resp.Body).Decode(&pair); err != nil {
		t.Fatal(err)
	}
	return pair
}
