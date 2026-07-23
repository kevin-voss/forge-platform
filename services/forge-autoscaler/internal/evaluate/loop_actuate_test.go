package evaluate_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

type fakeActuator struct {
	mu      sync.Mutex
	desired int
	has     bool
	calls   int
	ops     []string
}

func (f *fakeActuator) Get(context.Context, string, string, string) (actuate.ApplicationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return actuate.ApplicationView{DesiredReplicas: f.desired, HasDesired: f.has, ResourceVersion: "1"}, nil
}

func (f *fakeActuator) SetDesiredReplicas(_ context.Context, _, _, _ string, desired int, op string) (actuate.ApplicationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.ops = append(f.ops, op)
	f.desired = desired
	f.has = true
	return actuate.ApplicationView{DesiredReplicas: desired, HasDesired: true, ResourceVersion: "2"}, nil
}

func TestLoopScalesUpAndDownWithFakeMetrics(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_scale", Name: "scale", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "invoice-api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 4},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 2},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	row.Status.CurrentReplicas = 2

	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// High utilization → scale toward 4+; then low → scale down.
	fake.Push(row.Spec.TargetRef, "cpu", 130, 130, 20, 20, 20)
	act := &fakeActuator{desired: 2, has: true}
	loop := &evaluate.Loop{
		Store:      store,
		Source:     fake,
		Actuator:   act,
		Stabilizer: evaluate.NewStabilizer(),
		Now:        func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) },
	}

	loop.Tick(context.Background())
	refreshRV(store, row)
	if act.desired < 4 {
		t.Fatalf("expected scale-up to >=4, got %d", act.desired)
	}

	loop.Tick(context.Background())
	refreshRV(store, row)
	loop.Tick(context.Background())
	refreshRV(store, row)
	loop.Tick(context.Background())
	refreshRV(store, row)
	loop.Tick(context.Background())

	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.DesiredReplicas < row.Spec.MinReplicas || final.Status.DesiredReplicas > row.Spec.MaxReplicas {
		t.Fatalf("desired out of bounds: %d", final.Status.DesiredReplicas)
	}
	if act.desired < row.Spec.MinReplicas {
		t.Fatalf("actuator below min: %d", act.desired)
	}
	if final.Status.LastRecommendation == nil || final.Status.LastRecommendation.RecommendedReplicas == nil {
		t.Fatalf("expected lastRecommendation")
	}
}

func TestLoopRateLimitCapsDelta(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_rate", Name: "rate", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 2},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// 2 * 200/65 ≈ 7 → rate-limited to 2+2=4
	fake.Push(row.Spec.TargetRef, "cpu", 200)
	act := &fakeActuator{desired: 2, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 4 {
		t.Fatalf("expected rate-limited desired 4, got %d", act.desired)
	}
}

func TestLoopMetricOutageHoldsSafeDesired(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_hold", Name: "hold", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 5
	row.Status.CurrentReplicas = 5
	store := newMemStore(row)
	act := &fakeActuator{desired: 5, has: true}
	loop := &evaluate.Loop{
		Store:    store,
		Source:   errSource{err: metrics.ErrNotImplemented},
		Actuator: act,
	}
	loop.Tick(context.Background())
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.DesiredReplicas != 5 {
		t.Fatalf("expected hold at 5, got %d", final.Status.DesiredReplicas)
	}
	if act.calls != 0 {
		t.Fatalf("outage must not actuate down, calls=%d", act.calls)
	}
	var scaling *policy.Condition
	for i := range final.Status.Conditions {
		if final.Status.Conditions[i].Type == "ScalingActive" {
			scaling = &final.Status.Conditions[i]
		}
	}
	if scaling == nil || scaling.Status != "Unknown" {
		t.Fatalf("expected ScalingActive=Unknown, got %+v", scaling)
	}
}

func TestLoopStabilizationPreventsRapidDown(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_stab", Name: "stab", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 300, MaxReplicasPerMinute: 10},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 6
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "cpu", 65, 10) // hold then sudden drop
	act := &fakeActuator{desired: 6, has: true}
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := base
	loop := &evaluate.Loop{
		Store:      store,
		Source:     fake,
		Actuator:   act,
		Stabilizer: evaluate.NewStabilizer(),
		Now:        func() time.Time { return clock },
	}
	loop.Tick(context.Background())
	refreshRV(store, row)
	clock = base.Add(5 * time.Second)
	loop.Tick(context.Background())
	if act.desired != 6 {
		t.Fatalf("stabilization should hold at 6, got %d", act.desired)
	}
}

func refreshRV(store *memStore, row policy.Row) {
	store.mu.Lock()
	cur := store.rows[key(row.Project, row.Environment, row.Name)]
	store.rows[key(row.Project, row.Environment, row.Name)] = cur
	store.mu.Unlock()
}
