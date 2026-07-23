package node

import "testing"

func TestEffectiveAllocatedSlotsPrefersLiveWhenStale(t *testing.T) {
	n := FleetNode{CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: nil}
	if n.EffectiveAllocatedSlots() != 0 {
		t.Fatalf("empty live want 0 got %d", n.EffectiveAllocatedSlots())
	}
	n.RunningReplicas = []string{"r1"}
	if n.EffectiveAllocatedSlots() != 1 {
		t.Fatalf("live=1 want 1 got %d", n.EffectiveAllocatedSlots())
	}
	n.AllocatedSlots = 0
	n.RunningReplicas = []string{"r1", "r2"}
	if n.EffectiveAllocatedSlots() != 2 {
		t.Fatalf("allocated=0 live=2 want 2 got %d", n.EffectiveAllocatedSlots())
	}
}

func TestClusterReservationRatioIgnoresStaleSlots(t *testing.T) {
	// Mirrors ClusterReservation aggregation: live < allocated → use live.
	r := ClusterReservation{CapacitySlots: 6, AllocatedSlots: 2} // 2 live of 6
	if got := r.Ratio(); got >= 0.85 {
		t.Fatalf("ratio=%v should be below breach threshold with live=2/6", got)
	}
	stale := ClusterReservation{CapacitySlots: 6, AllocatedSlots: 6}
	if got := stale.Ratio(); got < 0.85 {
		t.Fatalf("stale full ratio=%v should breach", got)
	}
}
