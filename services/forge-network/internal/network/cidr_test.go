package network

import (
	"testing"
)

func TestNodeBlocksSequentialFromCluster16(t *testing.T) {
	plan, err := ParsePlan("10.100.0.0/16", 24)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NodeBlockCount() != 256 {
		t.Fatalf("count=%d", plan.NodeBlockCount())
	}

	first, err := plan.NodeBlock(0)
	if err != nil {
		t.Fatal(err)
	}
	if first.String() != "10.100.0.0/24" {
		t.Fatalf("first=%s", first)
	}

	second, err := plan.NodeBlock(1)
	if err != nil {
		t.Fatal(err)
	}
	if second.String() != "10.100.1.0/24" {
		t.Fatalf("second=%s", second)
	}

	last, err := plan.NodeBlock(255)
	if err != nil {
		t.Fatal(err)
	}
	if last.String() != "10.100.255.0/24" {
		t.Fatalf("last=%s", last)
	}
}

func TestWorkloadAddressesInBlock(t *testing.T) {
	plan, err := ParsePlan("10.100.0.0/16", 24)
	if err != nil {
		t.Fatal(err)
	}
	block, err := plan.NodeBlock(1)
	if err != nil {
		t.Fatal(err)
	}
	gw, err := GatewayForBlock(block)
	if err != nil {
		t.Fatal(err)
	}
	if gw.String() != "10.100.1.1" {
		t.Fatalf("gateway=%s", gw)
	}

	firstWL, err := WorkloadAddress(block, FirstWorkloadOffset)
	if err != nil {
		t.Fatal(err)
	}
	if firstWL.String() != "10.100.1.2" {
		t.Fatalf("first workload=%s", firstWL)
	}

	lastOff := MaxWorkloadOffset(block)
	if lastOff != 254 {
		t.Fatalf("last offset=%d", lastOff)
	}
	lastWL, err := WorkloadAddress(block, lastOff)
	if err != nil {
		t.Fatal(err)
	}
	if lastWL.String() != "10.100.1.254" {
		t.Fatalf("last workload=%s", lastWL)
	}
}

func TestOverlaps(t *testing.T) {
	ok, err := Overlaps("10.100.0.0/16", "10.100.1.0/24")
	if err != nil || !ok {
		t.Fatalf("expected overlap: %v %v", ok, err)
	}
	ok, err = Overlaps("10.100.0.0/16", "172.30.0.0/16")
	if err != nil || ok {
		t.Fatalf("expected no overlap: %v %v", ok, err)
	}
}
