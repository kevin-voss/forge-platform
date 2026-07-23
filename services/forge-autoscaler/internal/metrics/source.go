package metrics

import (
	"context"
	"errors"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// ErrNotImplemented is returned by stub adapters until later steps wire real queries.
var ErrNotImplemented = errors.New("metric source not implemented")

// Sample is one metric observation.
type Sample struct {
	Value      float64
	Target     float64
	ObservedAt time.Time
	Source     string
}

// MetricSource fetches a metric for a ScalingPolicy target.
type MetricSource interface {
	Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error)
}

// Router dispatches Fetch to the adapter that owns a metric type.
type Router struct {
	Observe MetricSource
	Gateway MetricSource
	Queue   MetricSource
	Runtime MetricSource
	Fake    MetricSource
	Prefer  string // "fake" forces Fake for all types
}

// Fetch implements MetricSource.
func (r *Router) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	if r.Prefer == "fake" && r.Fake != nil {
		return r.Fake.Fetch(ctx, target, metric)
	}
	switch strings.ToLower(strings.TrimSpace(metric.Type)) {
	case "cpu", "memory", "custom":
		if r.Observe != nil {
			return r.Observe.Fetch(ctx, target, metric)
		}
	case "requestrate", "request_rate", "latency", "errorrate", "error_rate":
		if r.Gateway != nil {
			return r.Gateway.Fetch(ctx, target, metric)
		}
	case "queuedepth", "queue_depth", "queue":
		if r.Queue != nil {
			return r.Queue.Fetch(ctx, target, metric)
		}
	default:
		if r.Runtime != nil {
			return r.Runtime.Fetch(ctx, target, metric)
		}
	}
	if r.Runtime != nil {
		return r.Runtime.Fetch(ctx, target, metric)
	}
	return Sample{}, ErrNotImplemented
}

// TargetAverage returns the configured target value for a metric, if any.
func TargetAverage(metric policy.MetricSpec) float64 {
	if metric.TargetAverageUtilization != nil {
		return *metric.TargetAverageUtilization
	}
	if metric.TargetValue != nil {
		return *metric.TargetValue
	}
	return 0
}
