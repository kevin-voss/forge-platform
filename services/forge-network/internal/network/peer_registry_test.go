package network

import (
	"context"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-network/internal/db"
)

func TestRotationKeepsOldKeyUntilRetire(t *testing.T) {
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
	reg := &PeerRegistry{
		Pool:             database.Pool,
		KeepaliveSeconds: 25,
		RotationWindow:   time.Minute,
		Metrics:          &PeerMetrics{},
	}
	comp := &PeerSetComputer{Registry: reg}

	name := "peers-rot-" + newID("n")
	row, err := alloc.CreateNetwork(ctx, name, "10.122.0.0/16", 24, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer cleanupPeers(t, database, row.ID)

	for _, node := range []string{"node-a", "node-b"} {
		if _, err := alloc.AllocateNodeLease(ctx, name, node); err != nil {
			t.Fatalf("lease: %v", err)
		}
		if _, err := reg.Register(ctx, name, node, "b64:old-"+node, "9.9.9.9:51820"); err != nil {
			t.Fatalf("reg: %v", err)
		}
		if _, err := comp.OnJoin(ctx, name, node); err != nil {
			t.Fatalf("join: %v", err)
		}
	}

	res, err := reg.RotateKey(ctx, name, "node-a", "b64:new-node-a")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if res.Status != PeerStatusRotating {
		t.Fatalf("status=%s", res.Status)
	}
	if _, err := comp.OnRotate(ctx, name, "node-a"); err != nil {
		t.Fatalf("onrotate: %v", err)
	}

	ps, err := comp.ComputeForNode(ctx, name, "node-b")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	keys := map[string]bool{}
	for _, p := range ps.Peers {
		if p.NodeID == "node-a" {
			keys[p.PublicKey] = true
		}
	}
	if !keys["b64:old-node-a"] || !keys["b64:new-node-a"] {
		t.Fatalf("expected dual keys during rotation, got %v", keys)
	}

	if err := reg.RetireOldKey(ctx, name, "node-a"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := comp.OnRetire(ctx, name, "node-a"); err != nil {
		t.Fatalf("onretire: %v", err)
	}
	ps2, err := comp.ComputeForNode(ctx, name, "node-b")
	if err != nil {
		t.Fatalf("compute2: %v", err)
	}
	keys2 := map[string]bool{}
	for _, p := range ps2.Peers {
		if p.NodeID == "node-a" {
			keys2[p.PublicKey] = true
		}
	}
	if keys2["b64:old-node-a"] {
		t.Fatal("old key should be removed after retire")
	}
	if !keys2["b64:new-node-a"] {
		t.Fatal("new key should remain after retire")
	}
}

func TestDriftMetric(t *testing.T) {
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
	metrics := &PeerMetrics{}
	reg := &PeerRegistry{Pool: database.Pool, Metrics: metrics}
	comp := &PeerSetComputer{Registry: reg}

	name := "peers-drift-" + newID("n")
	row, err := alloc.CreateNetwork(ctx, name, "10.123.0.0/16", 24, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer cleanupPeers(t, database, row.ID)

	for _, node := range []string{"node-a", "node-b"} {
		if _, err := alloc.AllocateNodeLease(ctx, name, node); err != nil {
			t.Fatalf("lease: %v", err)
		}
		if _, err := reg.Register(ctx, name, node, "b64:"+node, ""); err != nil {
			t.Fatalf("reg: %v", err)
		}
		if _, err := comp.OnJoin(ctx, name, node); err != nil {
			t.Fatalf("join: %v", err)
		}
	}

	drift, err := reg.DriftCount(ctx, name)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift != 2 {
		t.Fatalf("expected 2 drifting nodes, got %d", drift)
	}

	pa, _ := reg.getPeer(ctx, row.ID, "node-a")
	if err := reg.ReportAppliedVersion(ctx, name, "node-a", pa.PeerSetVersion); err != nil {
		t.Fatalf("applied a: %v", err)
	}
	drift, err = reg.DriftCount(ctx, name)
	if err != nil {
		t.Fatalf("drift2: %v", err)
	}
	if drift != 1 {
		t.Fatalf("expected 1 drifting node after a converges, got %d", drift)
	}

	pb, _ := reg.getPeer(ctx, row.ID, "node-b")
	if err := reg.ReportAppliedVersion(ctx, name, "node-b", pb.PeerSetVersion); err != nil {
		t.Fatalf("applied b: %v", err)
	}
	drift, err = reg.DriftCount(ctx, name)
	if err != nil {
		t.Fatalf("drift3: %v", err)
	}
	if drift != 0 {
		t.Fatalf("expected 0 drift when converged, got %d", drift)
	}
	if metrics.DriftTotal.Load() != 0 {
		t.Fatalf("metric drift=%d", metrics.DriftTotal.Load())
	}
}
