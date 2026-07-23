package evaluate_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

func TestLoopQueueBacklogBurstScalesWorkersUp(t *testing.T) {
	target := 500.0
	row := policy.Row{
		ID: "sp_worker", Name: "worker-scale", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Worker", Name: "invoice-worker"},
			MinReplicas: 1, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "queueDepth", TargetValue: &target, Queue: "invoice-jobs"}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 1
	row.Status.CurrentReplicas = 1

	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// 20_000 / 500 = 40 → clamped to max 20
	fake.Push(row.Spec.TargetRef, "queueDepth", 20000)
	act := &fakeActuator{desired: 1, has: true}
	tel := telemetry.NewRegistry()
	loop := &evaluate.Loop{
		Store:      store,
		Source:     fake,
		Actuator:   act,
		Stabilizer: evaluate.NewStabilizer(),
		Metrics:    tel,
		Now:        func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) },
	}

	loop.Tick(context.Background())
	if act.desired != 20 {
		t.Fatalf("expected scale-up to max 20, got %d", act.desired)
	}
	store.mu.Lock()
	up := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if up.Status.LastRecommendation == nil || !strings.Contains(up.Status.LastRecommendation.Reason, "ScaleUpQueue") {
		t.Fatalf("expected ScaleUpQueue reason, got %+v", up.Status.LastRecommendation)
	}
	body := metricBody(t, tel)
	if !strings.Contains(body, `forge_autoscaler_worker_desired_replicas{worker="invoice-worker"} 20`) {
		t.Fatalf("missing worker desired metric:\n%s", body)
	}
}

func TestLoopQueueDrainScalesWorkersDownAfterStabilization(t *testing.T) {
	target := 500.0
	row := policy.Row{
		ID: "sp_drain", Name: "drain", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Worker", Name: "invoice-worker"},
			MinReplicas: 1, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "queueDepth", TargetValue: &target, Queue: "invoice-jobs"}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 8
	row.Status.CurrentReplicas = 8
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "queueDepth", 0)
	act := &fakeActuator{desired: 8, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 1 {
		t.Fatalf("empty queue should scale down to minReplicas=1, got %d", act.desired)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.LastRecommendation == nil || !strings.Contains(final.Status.LastRecommendation.Reason, "ScaleDownQueue") {
		t.Fatalf("expected ScaleDownQueue, got %+v", final.Status.LastRecommendation)
	}
}

func TestLoopRetryPressureBlocksScaleDown(t *testing.T) {
	depthTarget := 500.0
	retryTarget := 0.05
	row := policy.Row{
		ID: "sp_retry", Name: "retry", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Worker", Name: "invoice-worker"},
			MinReplicas: 1, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{
				{Type: "queueDepth", TargetValue: &depthTarget, Queue: "invoice-jobs"},
				{Type: "retryRate", TargetValue: &retryTarget},
			},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 6
	row.Status.CurrentReplicas = 6
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// Empty backlog would scale to 1, but mild retry pressure must block down.
	fake.Push(row.Spec.TargetRef, "queueDepth", 0)
	fake.Push(row.Spec.TargetRef, "retryRate", 0.06)
	act := &fakeActuator{desired: 6, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 6 {
		t.Fatalf("retry pressure must hold at 6 (no scale-down), got %d", act.desired)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	found := false
	for _, c := range final.Status.Conditions {
		if c.Reason == "RetryPressureBlocksScaleDown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected RetryPressureBlocksScaleDown condition, got %+v", final.Status.Conditions)
	}
}

func TestLoopQueueMetricOutageHoldsWorkers(t *testing.T) {
	target := 500.0
	row := policy.Row{
		ID: "sp_qoutage", Name: "qoutage", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Worker", Name: "invoice-worker"},
			MinReplicas: 1, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "queueDepth", TargetValue: &target, Queue: "invoice-jobs"}},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 5
	row.Status.CurrentReplicas = 5
	store := newMemStore(row)
	act := &fakeActuator{desired: 5, has: true}
	loop := &evaluate.Loop{
		Store:    store,
		Source:   errSource{err: errors.Join(metrics.ErrUnavailable, errors.New("events down"))},
		Actuator: act,
	}
	loop.Tick(context.Background())
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.DesiredReplicas != 5 {
		t.Fatalf("outage must hold last desired 5, got %d", final.Status.DesiredReplicas)
	}
	if act.calls != 0 {
		t.Fatalf("outage must not actuate, calls=%d", act.calls)
	}
	if final.Status.Phase != "Degraded" {
		t.Fatalf("expected Degraded, got %s", final.Status.Phase)
	}
}

func TestLoopOldestAgeScalesWorkersUp(t *testing.T) {
	ageTarget := 30.0
	row := policy.Row{
		ID: "sp_age", Name: "age", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Worker", Name: "invoice-worker"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "oldestMessageAge", TargetValue: &ageTarget, Queue: "invoice-jobs"}},
			Behavior: policy.Behavior{
				ScaleUp: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 10},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "oldestMessageAge", 90)
	act := &fakeActuator{desired: 2, has: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired <= 2 {
		t.Fatalf("oldest-age breach should scale up, got %d", act.desired)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.LastRecommendation == nil || !strings.Contains(final.Status.LastRecommendation.Reason, "ScaleUpQueuePressure") {
		t.Fatalf("expected ScaleUpQueuePressure, got %+v", final.Status.LastRecommendation)
	}
}

func metricBody(t *testing.T, reg *telemetry.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}
