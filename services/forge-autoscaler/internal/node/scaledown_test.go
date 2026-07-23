package node

import (
	"context"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
)

func testPool(ready int) actuate.NodePoolView {
	return actuate.NodePoolView{
		Name: "docker-pool",
		Spec: map[string]any{
			"replicas":    ready,
			"machineType": "docker-small",
			"scaling":     map[string]any{"minNodes": 1, "maxNodes": 10},
		},
		Status: map[string]any{
			"readyNodes":   ready,
			"desiredNodes": ready,
			// Window already elapsed so EvaluateScaleDown can act immediately.
			"underutilizedNodeId": "node-b",
			"underutilizedSince":  time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
		},
	}
}

func TestEvaluateScaleDownSuccessEmptyNode(t *testing.T) {
	pool := testPool(3)
	fleet := []FleetNode{
		{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: []string{"r1", "r2"}},
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
		{ID: "node-c", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r3"}},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "scale_down" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
	if d.NodeID != "node-b" {
		t.Fatalf("node=%s want node-b", d.NodeID)
	}
	if d.DesiredNodes != 2 {
		t.Fatalf("desired=%d", d.DesiredNodes)
	}
	if d.OperationID == "" {
		t.Fatal("expected operation id")
	}
}

func TestEvaluateScaleDownStatefulPrimaryBlocked(t *testing.T) {
	pool := testPool(2)
	pool.Status["underutilizedNodeId"] = "node-primary"
	fleet := []FleetNode{
		{
			ID: "node-primary", Status: "online", CapacitySlots: 2, AllocatedSlots: 0,
			Labels: map[string]string{"forge.dev/stateful-role": "primary"},
		},
		{ID: "node-busy", Status: "online", CapacitySlots: 2, AllocatedSlots: 2},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "blocked" || d.Condition != "StatefulPrimaryProtected" {
		t.Fatalf("action=%s condition=%s reason=%s", d.Action, d.Condition, d.Reason)
	}
}

func TestEvaluateScaleDownDisruptionBudgetBlocked(t *testing.T) {
	pool := testPool(2)
	fleet := []FleetNode{
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
		{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r1"}},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:                   fleet,
		Pools:                   []actuate.NodePoolView{pool},
		UnderutilThreshold:      0.25,
		UnderutilWindow:         time.Minute,
		Now:                     time.Now().UTC(),
		DisruptionBudgetBlocked: true,
		DisruptionBudgetReason:  "DisruptionBudgetBlocked",
	})
	if d.Action != "blocked" || d.Condition != "DisruptionBudgetBlocked" {
		t.Fatalf("action=%s condition=%s", d.Action, d.Condition)
	}
}

func TestEvaluateScaleDownCanceledNoReplacement(t *testing.T) {
	pool := testPool(2)
	// Only underutilized node still has workloads; other node has no free slots.
	fleet := []FleetNode{
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r1"}},
		{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: []string{"r2", "r3"}},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.6, // 1/2 = 0.5 qualifies
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "canceled" || d.Condition != "ScaleDownCanceled" {
		t.Fatalf("action=%s condition=%s reason=%s", d.Action, d.Condition, d.Reason)
	}
	if d.DesiredNodes != 2 {
		t.Fatalf("desired must stay at 2, got %d", d.DesiredNodes)
	}
}

func TestEvaluateScaleDownRespectsMinNodes(t *testing.T) {
	pool := testPool(1)
	fleet := []FleetNode{
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "none" || d.Reason != "at_min_nodes" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
}

func TestEvaluateScaleDownPendingBlocks(t *testing.T) {
	pool := testPool(3)
	fleet := []FleetNode{{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0}}
	d := EvaluateScaleDown(ScaleDownInput{
		Pending:            []PendingWorkload{{PlacementID: "p1", Slots: 1}},
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "none" || d.Reason != "pending_demand" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
}

func TestEvaluateScaleDownIdempotentInFlight(t *testing.T) {
	pool := testPool(3)
	pool.Spec["replicas"] = 2
	pool.Status["desiredNodes"] = 2
	pool.Status["readyNodes"] = 3
	pool.Status["drainCandidateNodeId"] = "node-b"
	pool.Status["lastScaleDownOperationId"] = ScaleDownWindowID("docker-pool", "node-b")
	pool.Status["scaleDownPhase"] = "draining"
	fleet := []FleetNode{
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
		{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r1"}},
		{ID: "node-c", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r2"}},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "in_progress" && d.Action != "idempotent" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
	if d.OperationID != ScaleDownWindowID("docker-pool", "node-b") {
		t.Fatalf("op=%s", d.OperationID)
	}
}

func TestEvaluateScaleDownCancelInProgressUncordon(t *testing.T) {
	pool := testPool(3)
	pool.Spec["replicas"] = 2
	pool.Status["desiredNodes"] = 2
	pool.Status["readyNodes"] = 3
	pool.Status["drainCandidateNodeId"] = "node-b"
	pool.Status["lastScaleDownOperationId"] = ScaleDownWindowID("docker-pool", "node-b")
	pool.Status["scaleDownPhase"] = "draining"
	// Victim still has work and nowhere to go.
	fleet := []FleetNode{
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: []string{"r1", "r2"}},
		{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: []string{"r3", "r4"}},
		{ID: "node-c", Status: "online", CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: []string{"r5", "r6"}},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "canceled" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
	if d.DesiredNodes != 3 {
		t.Fatalf("uncordon desired=%d want 3", d.DesiredNodes)
	}
}

func TestApplyScaleDownLowersReplicasIdempotentOp(t *testing.T) {
	pool := testPool(3)
	pool.ResourceVersion = "1"
	fake := &fakePools{
		pools: map[string]actuate.NodePoolView{
			"docker-pool": pool,
		},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet: []FleetNode{
			{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
			{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r1"}},
			{ID: "node-c", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r2"}},
		},
		Pools:              []actuate.NodePoolView{fake.pools["docker-pool"]},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "scale_down" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
	if err := ApplyScaleDown(context.Background(), fake, d, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if fake.setReplicasCalls != 1 {
		t.Fatalf("SetReplicas calls=%d", fake.setReplicasCalls)
	}
	if fake.pools["docker-pool"].Spec["replicas"] != 2 {
		t.Fatalf("replicas=%v", fake.pools["docker-pool"].Spec["replicas"])
	}
	if actuate.StatusInt(fake.pools["docker-pool"], "desiredNodes") != 2 {
		t.Fatalf("desiredNodes=%d", actuate.StatusInt(fake.pools["docker-pool"], "desiredNodes"))
	}
	if actuate.StatusString(fake.pools["docker-pool"], "drainCandidateNodeId") != "node-b" {
		t.Fatalf("drain candidate=%s", actuate.StatusString(fake.pools["docker-pool"], "drainCandidateNodeId"))
	}
	if fake.startedContainers {
		t.Fatal("autoscaler must never start/delete containers")
	}

	// Re-apply same decision window → status only (idempotent).
	view := fake.pools["docker-pool"]
	d2 := EvaluateScaleDown(ScaleDownInput{
		Fleet: []FleetNode{
			{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
			{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r1"}},
			{ID: "node-c", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r2"}},
		},
		Pools:              []actuate.NodePoolView{view},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d2.Action != "in_progress" && d2.Action != "idempotent" {
		t.Fatalf("second action=%s", d2.Action)
	}
	if d2.OperationID != d.OperationID {
		t.Fatalf("op changed: %s vs %s", d2.OperationID, d.OperationID)
	}
}

func TestEvaluateScaleDownAllowsStaleCreatingNodes(t *testing.T) {
	// After scale-up Ready catches up, creatingNodes may linger at 1.
	// Scale-down must still proceed when ready >= desired.
	pool := testPool(3)
	pool.Status["creatingNodes"] = 1
	fleet := []FleetNode{
		{ID: "node-a", Status: "online", CapacitySlots: 2, AllocatedSlots: 2, RunningReplicas: []string{"r1", "r2"}},
		{ID: "node-b", Status: "online", CapacitySlots: 2, AllocatedSlots: 0},
		{ID: "node-c", Status: "online", CapacitySlots: 2, AllocatedSlots: 1, RunningReplicas: []string{"r3"}},
	}
	d := EvaluateScaleDown(ScaleDownInput{
		Fleet:              fleet,
		Pools:              []actuate.NodePoolView{pool},
		UnderutilThreshold: 0.25,
		UnderutilWindow:    time.Minute,
		Now:                time.Now().UTC(),
	})
	if d.Action != "scale_down" {
		t.Fatalf("action=%s reason=%s (stale creatingNodes must not block)", d.Action, d.Reason)
	}
	if d.NodeID != "node-b" {
		t.Fatalf("node=%s want node-b", d.NodeID)
	}
}

func TestLooksLikeStatefulPrimary(t *testing.T) {
	if !looksLikeStatefulPrimary(FleetNode{
		ID: "n1", Labels: map[string]string{"forge.dev/stateful-role": "primary"},
	}) {
		t.Fatal("expected label match")
	}
	if !looksLikeStatefulPrimary(FleetNode{
		ID: "n1", RunningReplicas: []string{"db/primary"},
	}) {
		t.Fatal("expected replica match")
	}
	if looksLikeStatefulPrimary(FleetNode{ID: "n1", RunningReplicas: []string{"web-1"}}) {
		t.Fatal("should not match")
	}
}
