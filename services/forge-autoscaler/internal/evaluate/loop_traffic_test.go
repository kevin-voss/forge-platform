package evaluate_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

func TestLoopRequestRateScalesUpAndDown(t *testing.T) {
	target := 150.0
	row := policy.Row{
		ID: "sp_rps", Name: "rps", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "invoice-api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "httpRequests", TargetValue: &target}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 10},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 10},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	row.Status.CurrentReplicas = 2

	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// 450 RPS / 150 = 3; then 100 RPS / 150 = 1 → clamped to min 2
	fake.Push(row.Spec.TargetRef, "httpRequests", 450, 100)
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
	if act.desired != 3 {
		t.Fatalf("expected scale-up to 3, got %d", act.desired)
	}
	store.mu.Lock()
	up := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if up.Status.LastRecommendation == nil || !strings.Contains(up.Status.LastRecommendation.Reason, "ScaleUpTraffic") {
		t.Fatalf("expected ScaleUpTraffic reason, got %+v", up.Status.LastRecommendation)
	}

	loop.Tick(context.Background())
	if act.desired != 2 {
		t.Fatalf("expected scale-down to min 2, got %d", act.desired)
	}
}

func TestLoopLatencyAndErrorNeverScaleDownAlone(t *testing.T) {
	latTarget := 0.20
	errTarget := 0.05
	row := policy.Row{
		ID: "sp_guard", Name: "guard", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{
				{Type: "p95Latency", TargetValue: &latTarget},
				{Type: "errorRate", TargetValue: &errTarget},
			},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 5},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 5},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 5
	row.Status.CurrentReplicas = 5
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// Healthy latency/error — must hold at 5, not scale down.
	fake.Push(row.Spec.TargetRef, "p95Latency", 0.05)
	fake.Push(row.Spec.TargetRef, "errorRate", 0.01)
	act := &fakeActuator{desired: 5, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 5 || act.calls != 0 {
		t.Fatalf("guardrail hold expected desired=5 calls=0, got desired=%d calls=%d", act.desired, act.calls)
	}

	refreshRV(store, row)
	fake.Push(row.Spec.TargetRef, "p95Latency", 0.50)
	fake.Push(row.Spec.TargetRef, "errorRate", 0.01)
	loop.Tick(context.Background())
	if act.desired <= 5 {
		t.Fatalf("latency breach should scale up, got %d", act.desired)
	}
}

func TestLoopMissingGatewayMetricContinuesCPU(t *testing.T) {
	cpuTarget := 65.0
	rpsTarget := 150.0
	row := policy.Row{
		ID: "sp_mixed", Name: "mixed", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{
				{Type: "cpu", TargetAverageUtilization: &cpuTarget},
				{Type: "httpRequests", TargetValue: &rpsTarget},
			},
			Behavior: policy.Behavior{
				ScaleUp: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 10},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// CPU high → recommend 4; httpRequests missing.
	fake.Push(row.Spec.TargetRef, "cpu", 130)
	src := &partialSource{fake: fake, failType: "httpRequests"}
	act := &fakeActuator{desired: 2, has: true}
	loop := &evaluate.Loop{Store: store, Source: src, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired < 4 {
		t.Fatalf("CPU should still scale up despite missing gateway metric, got %d", act.desired)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded phase for partial failure, got %s", final.Status.Phase)
	}
	foundUnavailable := false
	for _, rec := range final.Status.Recommendations {
		if rec.MetricType == "httpRequests" && strings.Contains(rec.Reason, "MetricUnavailable") {
			foundUnavailable = true
		}
	}
	if !foundUnavailable {
		t.Fatalf("expected MetricUnavailable recommendation, got %+v", final.Status.Recommendations)
	}
}

func TestLoopMixedMetricsTakesHighestRecommendation(t *testing.T) {
	cpuTarget := 65.0
	rpsTarget := 150.0
	row := policy.Row{
		ID: "sp_max", Name: "max", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{
				{Type: "cpu", TargetAverageUtilization: &cpuTarget},
				{Type: "httpRequests", TargetValue: &rpsTarget},
			},
			Behavior: policy.Behavior{
				ScaleUp: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// CPU → ceil(2*70/65)=3; RPS → ceil(900/150)=6 → dominant 6
	fake.Push(row.Spec.TargetRef, "cpu", 70)
	fake.Push(row.Spec.TargetRef, "httpRequests", 900)
	act := &fakeActuator{desired: 2, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 6 {
		t.Fatalf("expected highest recommendation 6, got %d", act.desired)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.LastRecommendation == nil || final.Status.LastRecommendation.MetricType != "httpRequests" {
		t.Fatalf("dominant metric should be httpRequests, got %+v", final.Status.LastRecommendation)
	}
}

func TestLoopLatencyInsufficientSamplesHolds(t *testing.T) {
	latTarget := 0.20
	row := policy.Row{
		ID: "sp_samples", Name: "samples", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "p95Latency", TargetValue: &latTarget}},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 3
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.PushSample(row.Spec.TargetRef, "p95Latency", metrics.Sample{Value: 0.9, SampleCount: 5})
	act := &fakeActuator{desired: 3, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 3 || act.calls != 0 {
		t.Fatalf("insufficient samples must hold, desired=%d calls=%d", act.desired, act.calls)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.LastRecommendation == nil || !strings.Contains(final.Status.LastRecommendation.Reason, "HoldInsufficientSamples") {
		t.Fatalf("expected HoldInsufficientSamples, got %+v", final.Status.LastRecommendation)
	}
}

type partialSource struct {
	fake     *metrics.FakeSource
	failType string
}

func (s *partialSource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (metrics.Sample, error) {
	if metrics.NormalizeMetricType(metric.Type) == metrics.NormalizeMetricType(s.failType) {
		return metrics.Sample{}, errors.Join(metrics.ErrUnavailable, errors.New("gateway metrics missing for route"))
	}
	return s.fake.Fetch(ctx, target, metric)
}
