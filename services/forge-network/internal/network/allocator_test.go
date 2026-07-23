package network

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-network/internal/db"
)

type staticSubnets struct {
	cidrs []string
	err   error
}

func (s staticSubnets) BridgeSubnets(context.Context) ([]string, error) {
	return s.cidrs, s.err
}

func TestCheckCollisionRejectsOverlap(t *testing.T) {
	a := &Allocator{
		Docker:        staticSubnets{cidrs: []string{"172.17.0.0/16", "10.100.0.0/16"}},
		ProviderCIDRs: nil,
	}
	err := a.CheckCollision(context.Background(), "10.100.0.0/16")
	if !errors.Is(err, ErrCidrCollision) {
		t.Fatalf("expected CidrCollision, got %v", err)
	}

	a2 := &Allocator{
		SkipDocker:    true,
		ProviderCIDRs: []string{"10.0.0.0/8"},
	}
	err = a2.CheckCollision(context.Background(), "10.100.0.0/16")
	if !errors.Is(err, ErrCidrCollision) {
		t.Fatalf("expected provider collision, got %v", err)
	}

	a3 := &Allocator{
		Docker:     staticSubnets{cidrs: []string{"172.30.0.0/16"}},
		SkipDocker: false,
	}
	if err := a3.CheckCollision(context.Background(), "10.100.0.0/16"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestAllocatorLeaseIdempotentReleaseReuse(t *testing.T) {
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := db.Open(ctx, dsn, "network", 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer database.Close()

	alloc := &Allocator{
		Pool:       database.Pool,
		SkipDocker: true,
	}

	name := "itest-overlay-" + newID("n")
	row, err := alloc.CreateNetwork(ctx, name, "10.110.0.0/16", 24, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.Phase != "Ready" {
		t.Fatalf("phase=%s", row.Phase)
	}
	defer func() {
		_, _ = database.Pool.Exec(ctx, `DELETE FROM network.workload_leases WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM network.node_leases WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM network.networks WHERE id=$1`, row.ID)
	}()

	l1, err := alloc.AllocateNodeLease(ctx, name, "node-a")
	if err != nil {
		t.Fatalf("alloc1: %v", err)
	}
	if l1.CIDR != "10.110.1.0/24" {
		t.Fatalf("cidr=%s", l1.CIDR)
	}
	l1b, err := alloc.AllocateNodeLease(ctx, name, "node-a")
	if err != nil {
		t.Fatalf("alloc idempotent: %v", err)
	}
	if l1b.CIDR != l1.CIDR {
		t.Fatalf("idempotent mismatch %s vs %s", l1.CIDR, l1b.CIDR)
	}

	l2, err := alloc.AllocateNodeLease(ctx, name, "node-b")
	if err != nil {
		t.Fatalf("alloc2: %v", err)
	}
	if l2.CIDR != "10.110.2.0/24" {
		t.Fatalf("cidr2=%s", l2.CIDR)
	}
	if l2.CIDR == l1.CIDR {
		t.Fatal("node blocks collided")
	}

	wl, err := alloc.AllocateWorkloadLease(ctx, name, "node-a", "wl_1")
	if err != nil {
		t.Fatalf("wl: %v", err)
	}
	ok, err := ContainsAddr(l1.CIDR, wl.Address)
	if err != nil || !ok {
		t.Fatalf("address %s not in %s", wl.Address, l1.CIDR)
	}
	wl2, err := alloc.AllocateWorkloadLease(ctx, name, "node-a", "wl_1")
	if err != nil {
		t.Fatalf("wl idempotent: %v", err)
	}
	if wl2.Address != wl.Address {
		t.Fatalf("wl mismatch")
	}

	if err := alloc.ReleaseNodeLease(ctx, name, "node-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	// node-b still holds .2; re-lease node-a should get a free block (prefer .1 again).
	l3, err := alloc.AllocateNodeLease(ctx, name, "node-a")
	if err != nil {
		t.Fatalf("re-lease: %v", err)
	}
	if l3.CIDR == l2.CIDR {
		t.Fatalf("re-lease collided with active node-b: %s", l3.CIDR)
	}
	if l3.CIDR != "10.110.1.0/24" && l3.CIDR != "10.110.3.0/24" {
		t.Fatalf("unexpected re-lease cidr %s", l3.CIDR)
	}
}

func TestCreateNetworkCollisionPersistsFailed(t *testing.T) {
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := db.Open(ctx, dsn, "network", 4, true)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer database.Close()

	alloc := &Allocator{
		Pool:          database.Pool,
		SkipDocker:    true,
		ProviderCIDRs: []string{"10.200.0.0/16"},
	}
	name := "itest-collide-" + newID("n")
	row, err := alloc.CreateNetwork(ctx, name, "10.200.0.0/16", 24, nil)
	if !errors.Is(err, ErrCidrCollision) {
		t.Fatalf("expected collision, got %v", err)
	}
	if row.Phase != "Failed" {
		t.Fatalf("phase=%s", row.Phase)
	}
	got, err := alloc.GetNetworkByName(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "Failed" || got.ConditionReason == nil || *got.ConditionReason != "CidrCollision" {
		t.Fatalf("persisted=%+v", got)
	}
	_, _ = database.Pool.Exec(ctx, `DELETE FROM network.networks WHERE id=$1`, got.ID)
}
