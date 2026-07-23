package evaluate_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"forge.local/services/forge-autoscaler/internal/evaluate"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

type memStore struct {
	mu   sync.Mutex
	rows map[string]policy.Row
}

func newMemStore(rows ...policy.Row) *memStore {
	m := &memStore{rows: map[string]policy.Row{}}
	for _, r := range rows {
		m.rows[key(r.Project, r.Environment, r.Name)] = r
	}
	return m
}

func key(p, e, n string) string { return p + "/" + e + "/" + n }

func (m *memStore) ListAll(context.Context) ([]policy.Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]policy.Row, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memStore) ReplaceStatus(_ context.Context, project, env, name string, expectedRV int64, status policy.ScalingPolicyStatus) (policy.Envelope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(project, env, name)
	row, ok := m.rows[k]
	if !ok {
		return policy.Envelope{}, policy.ErrNotFound
	}
	if row.ResourceVersion != expectedRV {
		return policy.Envelope{}, policy.ErrConflict
	}
	row.Status = status
	row.ResourceVersion++
	m.rows[k] = row
	return row.ToEnvelope(), nil
}

type errSource struct{ err error }

func (e errSource) Fetch(context.Context, policy.TargetRef, policy.MetricSpec) (metrics.Sample, error) {
	return metrics.Sample{}, e.err
}

func TestLoopAppendsBoundedRecommendations(t *testing.T) {
	util := 65.0
	row := policy.Row{
		ID: "sp_1", Name: "p1", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "demo"},
			MinReplicas: 2, MaxReplicas: 10,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
		},
		Status: policy.DefaultStatus(1),
	}
	store := newMemStore(row)
	fake := metrics.NewFakeSource()
	for i := 0; i < 15; i++ {
		fake.Push(row.Spec.TargetRef, "cpu", float64(50+i))
	}
	loop := &evaluate.Loop{Store: store, Source: fake}
	for i := 0; i < 12; i++ {
		loop.Tick(context.Background())
		// Refresh RV from store for next tick.
		store.mu.Lock()
		cur := store.rows[key(row.Project, row.Environment, row.Name)]
		store.mu.Unlock()
		_ = cur
	}
	store.mu.Lock()
	final := store.rows[key(row.Project, row.Environment, row.Name)]
	store.mu.Unlock()
	if got := len(final.Status.Recommendations); got != policy.MaxRecommendations {
		t.Fatalf("expected ring buffer cap %d, got %d", policy.MaxRecommendations, got)
	}
	if final.Status.Phase != "Ready" {
		t.Fatalf("expected Ready, got %s", final.Status.Phase)
	}
}

func TestLoopMetricErrorSetsUnknownAndContinues(t *testing.T) {
	util := 65.0
	good := policy.Row{
		ID: "sp_good", Name: "good", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "good"},
			MinReplicas: 1, MaxReplicas: 5,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
		},
		Status: policy.DefaultStatus(1),
	}
	bad := policy.Row{
		ID: "sp_bad", Name: "bad", Project: "demo", Environment: "production",
		Generation: 1, ResourceVersion: 1,
		Spec: policy.ScalingPolicySpec{
			TargetRef:   policy.TargetRef{Kind: "Application", Name: "bad"},
			MinReplicas: 1, MaxReplicas: 5,
			Metrics: []policy.MetricSpec{{Type: "cpu", TargetAverageUtilization: &util}},
		},
		Status: policy.DefaultStatus(1),
	}
	store := newMemStore(good, bad)
	fake := metrics.NewFakeSource()
	fake.Push(good.Spec.TargetRef, "cpu", 40)

	src := &selectiveSource{fake: fake, failName: "bad"}
	loop := &evaluate.Loop{Store: store, Source: src}
	loop.Tick(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	badRow := store.rows[key(bad.Project, bad.Environment, bad.Name)]
	goodRow := store.rows[key(good.Project, good.Environment, good.Name)]

	var scaling *policy.Condition
	for i := range badRow.Status.Conditions {
		if badRow.Status.Conditions[i].Type == "ScalingActive" {
			scaling = &badRow.Status.Conditions[i]
		}
	}
	if scaling == nil || scaling.Status != "Unknown" || scaling.Reason != "MetricFetchFailed" {
		t.Fatalf("expected ScalingActive=Unknown/MetricFetchFailed, got %+v", scaling)
	}
	if len(goodRow.Status.Recommendations) != 1 {
		t.Fatalf("good policy should still record a recommendation, got %d", len(goodRow.Status.Recommendations))
	}
}

type selectiveSource struct {
	fake     *metrics.FakeSource
	failName string
}

func (s *selectiveSource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (metrics.Sample, error) {
	if target.Name == s.failName {
		return metrics.Sample{}, errors.New("boom")
	}
	return s.fake.Fetch(ctx, target, metric)
}
