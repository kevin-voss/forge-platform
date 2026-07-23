package network

import (
	"context"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-network/internal/db"
)

func TestPeerSetComputerFullMeshAndOfflineExcluded(t *testing.T) {
	online := []string{"node-a", "node-b", "node-c"}
	got := OfflineNodeExcluded(online, "node-c")
	if len(got["node-a"]) != 1 || got["node-a"][0] != "node-b" {
		t.Fatalf("node-a peers=%v", got["node-a"])
	}
	if _, ok := got["node-c"]; ok {
		t.Fatal("offline node should not appear as a peer-set owner")
	}

	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := db.Open(ctx, dsn, "network", 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer database.Close()

	alloc := &Allocator{Pool: database.Pool, SkipDocker: true}
	reg := &PeerRegistry{Pool: database.Pool, KeepaliveSeconds: 25, MTU: 1420, Metrics: &PeerMetrics{}}
	comp := &PeerSetComputer{Registry: reg}

	name := "peers-mesh-" + newID("n")
	row, err := alloc.CreateNetwork(ctx, name, "10.120.0.0/16", 24, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer cleanupPeers(t, database, row.ID)

	for _, node := range []string{"node-a", "node-b", "node-c"} {
		if _, err := alloc.AllocateNodeLease(ctx, name, node); err != nil {
			t.Fatalf("lease %s: %v", node, err)
		}
		if _, err := reg.Register(ctx, name, node, "b64:key-"+node, "10.0.0.1:51820"); err != nil {
			t.Fatalf("register %s: %v", node, err)
		}
		if _, err := comp.OnJoin(ctx, name, node); err != nil {
			t.Fatalf("onjoin %s: %v", node, err)
		}
	}

	for _, node := range []string{"node-a", "node-b", "node-c"} {
		ps, err := comp.ComputeForNode(ctx, name, node)
		if err != nil {
			t.Fatalf("compute %s: %v", node, err)
		}
		if len(ps.Peers) != 2 {
			t.Fatalf("%s expected 2 peers, got %d", node, len(ps.Peers))
		}
		for _, p := range ps.Peers {
			if len(p.AllowedIPs) != 1 {
				t.Fatalf("allowed_ips=%v", p.AllowedIPs)
			}
			if p.PersistentKeepalive != 25 {
				t.Fatalf("keepalive=%d", p.PersistentKeepalive)
			}
		}
	}

	if err := reg.SetOnline(ctx, name, "node-c", false); err != nil {
		t.Fatalf("offline: %v", err)
	}
	// Offline node is excluded; remaining nodes still see each other only.
	psA, err := comp.ComputeForNode(ctx, name, "node-a")
	if err != nil {
		t.Fatalf("compute a: %v", err)
	}
	if len(psA.Peers) != 1 || psA.Peers[0].NodeID != "node-b" {
		t.Fatalf("after offline, node-a peers=%+v", psA.Peers)
	}
}

func TestPeerSetComputerIncrementalVersionBump(t *testing.T) {
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	database, err := db.Open(ctx, dsn, "network", 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer database.Close()

	alloc := &Allocator{Pool: database.Pool, SkipDocker: true}
	reg := &PeerRegistry{Pool: database.Pool, Metrics: &PeerMetrics{}}
	comp := &PeerSetComputer{Registry: reg}

	name := "peers-incr-" + newID("n")
	row, err := alloc.CreateNetwork(ctx, name, "10.121.0.0/16", 24, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer cleanupPeers(t, database, row.ID)

	for _, node := range []string{"node-a", "node-b", "node-c"} {
		if _, err := alloc.AllocateNodeLease(ctx, name, node); err != nil {
			t.Fatalf("lease: %v", err)
		}
		if _, err := reg.Register(ctx, name, node, "b64:"+node, "1.1.1.1:51820"); err != nil {
			t.Fatalf("reg: %v", err)
		}
		if _, err := comp.OnJoin(ctx, name, node); err != nil {
			t.Fatalf("join: %v", err)
		}
	}

	// Mark node-c offline so it is not affected by a later join.
	if err := reg.SetOnline(ctx, name, "node-c", false); err != nil {
		t.Fatalf("offline c: %v", err)
	}
	before := map[string]int64{}
	for _, node := range []string{"node-a", "node-b", "node-c"} {
		p, err := reg.getPeer(ctx, row.ID, node)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		before[node] = p.PeerSetVersion
	}

	if _, err := alloc.AllocateNodeLease(ctx, name, "node-d"); err != nil {
		t.Fatalf("lease d: %v", err)
	}
	if _, err := reg.Register(ctx, name, "node-d", "b64:node-d", "2.2.2.2:51820"); err != nil {
		t.Fatalf("reg d: %v", err)
	}
	if _, err := comp.OnJoin(ctx, name, "node-d"); err != nil {
		t.Fatalf("join d: %v", err)
	}

	after := map[string]int64{}
	for _, node := range []string{"node-a", "node-b", "node-c", "node-d"} {
		p, err := reg.getPeer(ctx, row.ID, node)
		if err != nil {
			t.Fatalf("get after: %v", err)
		}
		after[node] = p.PeerSetVersion
	}
	if after["node-a"] <= before["node-a"] || after["node-b"] <= before["node-b"] {
		t.Fatalf("online nodes should bump: before=%v after=%v", before, after)
	}
	if after["node-c"] != before["node-c"] {
		t.Fatalf("offline node-c must not bump: before=%d after=%d", before["node-c"], after["node-c"])
	}
	if after["node-d"] < 1 {
		t.Fatalf("node-d version=%d", after["node-d"])
	}
}

func cleanupPeers(t *testing.T, database *db.DB, networkID string) {
	t.Helper()
	ctx := context.Background()
	_, _ = database.Pool.Exec(ctx, `DELETE FROM network.wireguard_peers WHERE network_id=$1`, networkID)
	_, _ = database.Pool.Exec(ctx, `DELETE FROM network.workload_leases WHERE network_id=$1`, networkID)
	_, _ = database.Pool.Exec(ctx, `DELETE FROM network.node_leases WHERE network_id=$1`, networkID)
	_, _ = database.Pool.Exec(ctx, `DELETE FROM network.networks WHERE id=$1`, networkID)
}
