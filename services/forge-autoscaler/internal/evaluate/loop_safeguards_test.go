package evaluate_test

import (
	"context"
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
	"forge.local/services/forge-autoscaler/internal/audit"
	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

type progressingActuator struct {
	fakeActuator
	progressing bool
}

func (p *progressingActuator) Get(ctx context.Context, project, env, kind, name string) (actuate.WorkloadView, error) {
	view, err := p.fakeActuator.Get(ctx, project, env, kind, name)
	view.Progressing = p.progressing
	if p.progressing {
		view.Phase = "Progressing"
	}
	return view, err
}

func TestLoopScheduleRaisesMinAtCorrectTime(t *testing.T) {
	util := 65.0
	minSched := 8
	row := policy.Row{
		ID: "sp_sched", Name: "sched", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
			Schedules: []policy.Schedule{
				{Name: "business", Cron: "* 7-19 * * *", MinReplicas: &minSched},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	row.Status.CurrentReplicas = 2
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	// Low util would want ~2, but schedule floor is 8.
	fake.Push(row.Spec.TargetRef, "cpu", 20)
	act := &fakeActuator{desired: 2, has: true}
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	loop := &evaluate.Loop{
		Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer(),
		Now: func() time.Time { return now },
	}
	loop.Tick(context.Background())
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.DesiredReplicas != 8 {
		t.Fatalf("expected schedule min 8, got %d", final.Status.DesiredReplicas)
	}
	if len(final.Status.ActiveSchedules) != 1 || final.Status.ActiveSchedules[0] != "business" {
		t.Fatalf("active schedules: %v", final.Status.ActiveSchedules)
	}
	if act.desired != 8 {
		t.Fatalf("actuator desired=%d", act.desired)
	}
}

func TestLoopOverlappingSchedulesMerge(t *testing.T) {
	util := 65.0
	minA, maxA := 10, 40
	minB, maxB := 4, 12
	row := policy.Row{
		ID: "sp_overlap", Name: "overlap", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 50,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 50},
			},
			Schedules: []policy.Schedule{
				{Name: "peak", Cron: "* * * * *", MinReplicas: &minA, MaxReplicas: &maxA},
				{Name: "cap", Cron: "* * * * *", MinReplicas: &minB, MaxReplicas: &maxB},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "cpu", 200) // would want many replicas
	act := &fakeActuator{desired: 2, has: true}
	loop := &evaluate.Loop{
		Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer(),
		Now: func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) },
	}
	loop.Tick(context.Background())
	if act.desired > 12 {
		t.Fatalf("expected merged max 12, got %d", act.desired)
	}
	if act.desired < 10 {
		t.Fatalf("expected merged min 10, got %d", act.desired)
	}
}

func TestLoopManualOverrideSupersedesMetricsUntilExpiry(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_ov", Name: "ov", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 2
	row.Status.ManualOverride = &policy.ManualOverride{
		Replicas:  9,
		Reason:    "incident",
		ExpiresAt: "2026-07-23T13:00:00Z",
		CreatedBy: "oncall",
	}
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "cpu", 20) // would stay low
	events := &audit.Memory{}
	act := &fakeActuator{desired: 2, has: true}
	tel := telemetry.NewRegistry()
	activeNow := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	loop := &evaluate.Loop{
		Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer(),
		Events: events, Metrics: tel,
		Now: func() time.Time { return activeNow },
	}
	loop.Tick(context.Background())
	if act.desired != 9 {
		t.Fatalf("override should force 9, got %d", act.desired)
	}

	// After TTL: override clears and metrics resume.
	refreshRV(store, row)
	store.mu.Lock()
	cur := store.rows[key(row.Project, row.Environment, row.Name)]
	cur.Status.ManualOverride = &policy.ManualOverride{
		Replicas: 9, Reason: "incident", ExpiresAt: "2026-07-23T13:00:00Z", CreatedBy: "oncall",
	}
	store.rows[key(row.Project, row.Environment, row.Name)] = cur
	store.mu.Unlock()

	fake.Push(row.Spec.TargetRef, "cpu", 20)
	expiredNow := time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC)
	loop.Now = func() time.Time { return expiredNow }
	loop.Tick(context.Background())
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.ManualOverride != nil {
		t.Fatal("expected override cleared after expiry")
	}
	foundExpired := false
	for _, e := range events.Snapshot() {
		if e.Type == audit.OverrideExpired {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatal("expected autoscaling.override.expired event")
	}
	var auditExpired bool
	for _, a := range final.Status.Audit {
		if a.Type == "override.expired" {
			auditExpired = true
		}
	}
	if !auditExpired {
		t.Fatalf("expected audit override.expired, got %+v", final.Status.Audit)
	}
}

func TestLoopDeploymentFreezeBlocksScaleDownAllowsUp(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_freeze", Name: "freeze", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleUp:   policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
			DeploymentFreeze: &policy.DeploymentFreeze{Enabled: true},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 6
	row.Status.CurrentReplicas = 6
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "cpu", 10) // wants scale-down
	act := &fakeActuator{desired: 6, has: true}
	loop := &evaluate.Loop{
		Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer(),
	}
	loop.Tick(context.Background())
	if act.desired != 6 {
		t.Fatalf("freeze must block scale-down, got %d", act.desired)
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if !final.Status.DeploymentFrozen {
		t.Fatal("expected deploymentFrozen=true")
	}

	// Scale-up still allowed during freeze.
	refreshRV(store, row)
	fake.Push(row.Spec.TargetRef, "cpu", 200)
	loop.Tick(context.Background())
	if act.desired <= 6 {
		t.Fatalf("freeze must allow scale-up, got %d", act.desired)
	}
}

func TestLoopWorkloadRolloutBlocksScaleDown(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_roll", Name: "roll", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 20,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			Behavior: policy.Behavior{
				ScaleDown: policy.ScaleBehavior{StabilizationWindowSeconds: 0, MaxReplicasPerMinute: 20},
			},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 5
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	fake.Push(row.Spec.TargetRef, "cpu", 10)
	act := &progressingActuator{fakeActuator: fakeActuator{desired: 5, has: true}, progressing: true}
	loop := &evaluate.Loop{Store: store, Source: fake, Actuator: act, Stabilizer: evaluate.NewStabilizer()}
	loop.Tick(context.Background())
	if act.desired != 5 {
		t.Fatalf("rollout freeze should hold at 5, got %d", act.desired)
	}
}

func TestLoopMetricOutageModesExplicitInStatus(t *testing.T) {
	util := 65.0
	fixed := 7
	row := policy.Row{
		ID: "sp_outage", Name: "outage", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "api"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics:              []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
			MetricOutageFallback: &policy.MetricOutageFallback{Mode: policy.OutageFixed, FixedReplicas: &fixed},
		},
		Status: policy.DefaultStatus(1),
	}
	row.Status.DesiredReplicas = 5
	store := newMemStore(row)
	act := &fakeActuator{desired: 5, has: true}
	loop := &evaluate.Loop{Store: store, Source: errSource{err: metrics.ErrUnavailable}, Actuator: act}
	loop.Tick(context.Background())
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.MetricOutageMode != policy.OutageFixed {
		t.Fatalf("expected mode=fixed, got %q", final.Status.MetricOutageMode)
	}
	if final.Status.DesiredReplicas != 7 {
		t.Fatalf("expected fixed 7, got %d", final.Status.DesiredReplicas)
	}

	// floor mode
	refreshRV(store, row)
	store.mu.Lock()
	cur := store.rows[key(row.Project, row.Environment, row.Name)]
	cur.Spec.MetricOutageFallback = &policy.MetricOutageFallback{Mode: policy.OutageFloor}
	cur.Status.DesiredReplicas = 5
	store.rows[key(row.Project, row.Environment, row.Name)] = cur
	store.mu.Unlock()
	loop.Tick(context.Background())
	store.mu.Lock()
	final = store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if final.Status.MetricOutageMode != policy.OutageFloor || final.Status.DesiredReplicas != 2 {
		t.Fatalf("expected floor to min=2 mode=floor, got desired=%d mode=%s",
			final.Status.DesiredReplicas, final.Status.MetricOutageMode)
	}
}
