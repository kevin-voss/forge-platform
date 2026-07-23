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
	queue map[string][]float64
}

// NewFakeSource creates an empty FakeSource.
func NewFakeSource() *FakeSource {
	return &FakeSource{queue: map[string][]float64{}}
}

// Push enqueues values for a target+metricType key (FIFO).
func (f *FakeSource) Push(target policy.TargetRef, metricType string, values ...float64) {
	key := fakeKey(target, metricType)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue[key] = append(f.queue[key], values...)
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
	return Sample{
		Value:      v,
		Target:     TargetAverage(metric),
		ObservedAt: time.Now().UTC(),
		Source:     "fake",
	}, nil
}

func fakeKey(target policy.TargetRef, metricType string) string {
	return target.Kind + "/" + target.Name + "/" + metricType
}
