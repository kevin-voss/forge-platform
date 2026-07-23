package node

import (
	"context"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
)

func TestEvaluateScaleUpPendingCPUMemoryGPU(t *testing.T) {
	pools := []actuate.NodePoolView{
		{
			Name: "gpu-pool",
			Spec: map[string]any{
				"replicas":    1,
				"priority":    10,
				"machineType": "docker-medium",
				"machine":     map[string]any{"gpu": 1, "architecture": "amd64"},
				"scaling":     map[string]any{"maxNodes": 5},
				"region":      "local",
			},
			Status: map[string]any{"readyNodes": 1},
		},
		{
			Name: "cpu-pool",
			Spec: map[string]any{
				"replicas":    1,
				"priority":    1,
				"machineType": "docker-small",
				"scaling":     map[string]any{"maxNodes": 10},
				"region":      "local",
				"labels":      map[string]any{"workload-class": "general"},
			},
			Status: map[string]any{"readyNodes": 1},
		},
	}

	t.Run("cpu pending selects lowest priority eligible", func(t *testing.T) {
		d := EvaluateScaleUp(ScaleUpInput{
			Pending: []PendingWorkload{{PlacementID: "p1", Slots: 2, DeploymentID: "d1"}},
			Pools:   pools,
			Now:     time.Now().UTC(),
		})
		if d.Action != "scale_up" {
			t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
		}
		if d.PoolName != "cpu-pool" {
			t.Fatalf("pool=%s want cpu-pool", d.PoolName)
		}
		if d.DesiredNodes != 2 { // 1 ready + ceil(2/2)=1
			t.Fatalf("desired=%d", d.DesiredNodes)
		}
		if d.OperationID == "" {
			t.Fatal("expected operation id")
		}
	})

	t.Run("gpu demand selects gpu pool", func(t *testing.T) {
		d := EvaluateScaleUp(ScaleUpInput{
			Pending: []PendingWorkload{{PlacementID: "g1", Slots: 1, GPU: 1, Reason: "InsufficientGPU"}},
			Pools:   pools,
			Now:     time.Now().UTC(),
		})
		if d.Action != "scale_up" || d.PoolName != "gpu-pool" {
			t.Fatalf("action=%s pool=%s", d.Action, d.PoolName)
		}
	})

	t.Run("memory-ish multi-slot demand", func(t *testing.T) {
		d := EvaluateScaleUp(ScaleUpInput{
			Pending: []PendingWorkload{
				{PlacementID: "a", Slots: 3},
				{PlacementID: "b", Slots: 2},
			},
			Pools: pools,
			Now:   time.Now().UTC(),
		})
		// cpu-pool slotsPerNode=2 → ceil(5/2)=3 additional → desired 1+3=4
		if d.DesiredNodes != 4 {
			t.Fatalf("desired=%d want 4", d.DesiredNodes)
		}
	})
}

func TestEvaluateScaleUpNoEligiblePool(t *testing.T) {
	pools := []actuate.NodePoolView{
		{
			Name: "arm-only",
			Spec: map[string]any{
				"replicas": 1,
				"machine":  map[string]any{"architecture": "arm64"},
				"scaling":  map[string]any{"maxNodes": 3},
			},
			Status: map[string]any{"readyNodes": 1},
		},
	}
	d := EvaluateScaleUp(ScaleUpInput{
		Pending: []PendingWorkload{{PlacementID: "x", Slots: 1, Architecture: "amd64"}},
		Pools:   pools,
		Now:     time.Now().UTC(),
	})
	if d.Action != "no_eligible" || d.Condition != "NoEligibleNodePool" {
		t.Fatalf("got action=%s condition=%s", d.Action, d.Condition)
	}
}

func TestEvaluateScaleUpIdempotentDemandWindow(t *testing.T) {
	pending := []PendingWorkload{{PlacementID: "p1", Slots: 2}}
	op := DemandWindowID("cpu-pool", pending)
	pool := actuate.NodePoolView{
		Name: "cpu-pool",
		Spec: map[string]any{
			"replicas":    2,
			"machineType": "docker-small",
			"scaling":     map[string]any{"maxNodes": 10},
		},
		Status: map[string]any{
			"readyNodes":             1,
			"desiredNodes":           2,
			"lastScaleUpOperationId": op,
			"creatingNodes":          1,
		},
	}
	d := EvaluateScaleUp(ScaleUpInput{
		Pending: pending,
		Pools:   []actuate.NodePoolView{pool},
		Now:     time.Now().UTC(),
	})
	if d.Action != "idempotent" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
	if d.OperationID != op {
		t.Fatalf("op=%s want %s", d.OperationID, op)
	}
}

func TestEvaluateScaleUpReservationThreshold(t *testing.T) {
	pools := []actuate.NodePoolView{{
		Name: "cpu-pool",
		Spec: map[string]any{
			"replicas":    1,
			"machineType": "docker-small",
			"scaling":     map[string]any{"maxNodes": 5},
		},
		Status: map[string]any{"readyNodes": 1},
	}}
	d := EvaluateScaleUp(ScaleUpInput{
		Pending: nil,
		Reservation: ClusterReservation{
			CapacitySlots:  10,
			AllocatedSlots: 9,
		},
		ReservationThreshold: 0.85,
		Pools:                pools,
		Now:                  time.Now().UTC(),
	})
	if d.Action != "scale_up" {
		t.Fatalf("action=%s reason=%s", d.Action, d.Reason)
	}
}

func TestApplyScaleUpDoesNotStartContainers(t *testing.T) {
	fake := &fakePools{
		pools: map[string]actuate.NodePoolView{
			"cpu-pool": {
				Name:            "cpu-pool",
				ResourceVersion: "1",
				Spec: map[string]any{
					"replicas":    1,
					"machineType": "docker-small",
					"scaling":     map[string]any{"maxNodes": 5},
				},
				Status: map[string]any{"readyNodes": 1},
			},
		},
	}
	d := EvaluateScaleUp(ScaleUpInput{
		Pending: []PendingWorkload{{PlacementID: "p1", Slots: 2}},
		Pools:   []actuate.NodePoolView{fake.pools["cpu-pool"]},
		Now:     time.Now().UTC(),
	})
	if err := ApplyScaleUp(context.Background(), fake, d, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if fake.setReplicasCalls != 1 {
		t.Fatalf("SetReplicas calls=%d", fake.setReplicasCalls)
	}
	if fake.pools["cpu-pool"].Spec["replicas"] != 2 {
		t.Fatalf("replicas=%v", fake.pools["cpu-pool"].Spec["replicas"])
	}
	if actuate.StatusInt(fake.pools["cpu-pool"], "desiredNodes") != 2 {
		t.Fatalf("desiredNodes=%d", actuate.StatusInt(fake.pools["cpu-pool"], "desiredNodes"))
	}
	if fake.startedContainers {
		t.Fatal("autoscaler must never start containers")
	}
}

type fakePools struct {
	pools             map[string]actuate.NodePoolView
	setReplicasCalls  int
	startedContainers bool
}

func (f *fakePools) List(context.Context) ([]actuate.NodePoolView, error) {
	out := make([]actuate.NodePoolView, 0, len(f.pools))
	for _, p := range f.pools {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakePools) Get(_ context.Context, name string) (actuate.NodePoolView, error) {
	p, ok := f.pools[name]
	if !ok {
		return actuate.NodePoolView{}, actuate.ErrNodePoolNotFound
	}
	return p, nil
}

func (f *fakePools) SetReplicas(_ context.Context, name string, replicas int, _ string) (actuate.NodePoolView, error) {
	f.setReplicasCalls++
	p := f.pools[name]
	p.Spec["replicas"] = replicas
	p.ResourceVersion = p.ResourceVersion + "1"
	f.pools[name] = p
	return p, nil
}

func (f *fakePools) PutStatus(_ context.Context, name, _ string, status map[string]any) (actuate.NodePoolView, error) {
	p := f.pools[name]
	p.Status = status
	p.ResourceVersion = p.ResourceVersion + "1"
	f.pools[name] = p
	return p, nil
}
