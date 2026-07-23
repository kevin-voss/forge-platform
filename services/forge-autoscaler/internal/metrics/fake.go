package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// FakeSource returns scripted values per (target, metric type) key.
type FakeSource struct {
	mu    sync.Mutex
	queue map[string][]Sample
}

// NewFakeSource creates an empty FakeSource.
func NewFakeSource() *FakeSource {
	return &FakeSource{queue: map[string][]Sample{}}
}

// Push enqueues values for a target+metricType key (FIFO).
// SampleCount defaults to DefaultMinSampleCount so guardrail metrics can scale in tests.
func (f *FakeSource) Push(target policy.TargetRef, metricType string, values ...float64) {
	key := fakeKey(target, metricType)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range values {
		f.queue[key] = append(f.queue[key], Sample{
			Value:       v,
			SampleCount: DefaultMinSampleCount,
		})
	}
}

// PushSample enqueues full samples (including SampleCount) for tests.
func (f *FakeSource) PushSample(target policy.TargetRef, metricType string, samples ...Sample) {
	key := fakeKey(target, metricType)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue[key] = append(f.queue[key], samples...)
}

// Fetch implements MetricSource.
func (f *FakeSource) Fetch(_ context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	key := fakeKey(target, metric.Type)
	f.mu.Lock()
	defer f.mu.Unlock()
	values := f.queue[key]
	if len(values) == 0 {
		return Sample{}, fmt.Errorf("fake metric queue exhausted for %s", key)
	}
	v := values[0]
	f.queue[key] = values[1:]
	if v.Target == 0 {
		v.Target = TargetAverage(metric)
	}
	if v.ObservedAt.IsZero() {
		v.ObservedAt = time.Now().UTC()
	}
	if v.Source == "" {
		v.Source = "fake"
	}
	return v, nil
}

func fakeKey(target policy.TargetRef, metricType string) string {
	return target.Kind + "/" + target.Name + "/" + metricType
}
