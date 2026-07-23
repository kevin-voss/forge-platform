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

// ErrUnavailable means the metric type is known but missing for this route/target.
// Evaluation continues with remaining metrics.
var ErrUnavailable = errors.New("metric unavailable")

// DefaultMinSampleCount is the minimum request sample count required before
// latency/error-rate metrics may recommend a scale-up.
const DefaultMinSampleCount int64 = 50

// Sample is one metric observation.
type Sample struct {
	Value       float64
	Target      float64
	ObservedAt  time.Time
	Source      string
	SampleCount int64
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
	switch NormalizeMetricType(metric.Type) {
	case "cpu", "memory", "custom":
		if r.Observe != nil {
			sample, err := r.Observe.Fetch(ctx, target, metric)
			if err == nil {
				return sample, nil
			}
			// Local / degraded path: fall back to Runtime for cpu/memory only.
			if isCPUOrMemory(metric.Type) && r.Runtime != nil {
				fallback, ferr := r.Runtime.Fetch(ctx, target, metric)
				if ferr == nil {
					return fallback, nil
				}
			}
			return Sample{}, err
		}
		if isCPUOrMemory(metric.Type) && r.Runtime != nil {
			return r.Runtime.Fetch(ctx, target, metric)
		}
	case "httpRequests", "activeConnections":
		// Gateway is primary; Observe provides historical-window fallback.
		var gatewayErr error
		if r.Gateway != nil {
			sample, err := r.Gateway.Fetch(ctx, target, metric)
			if err == nil {
				return sample, nil
			}
			gatewayErr = err
		}
		if r.Observe != nil {
			sample, err := r.Observe.Fetch(ctx, target, metric)
			if err == nil {
				return sample, nil
			}
			if gatewayErr != nil {
				return Sample{}, gatewayErr
			}
			return Sample{}, err
		}
		if gatewayErr != nil {
			return Sample{}, gatewayErr
		}
	case "p95Latency", "errorRate":
		// Observe is primary for latency/error historical windows; Gateway optional.
		if r.Observe != nil {
			sample, err := r.Observe.Fetch(ctx, target, metric)
			if err == nil {
				return sample, nil
			}
			if r.Gateway != nil {
				fallback, ferr := r.Gateway.Fetch(ctx, target, metric)
				if ferr == nil {
					return fallback, nil
				}
			}
			return Sample{}, err
		}
		if r.Gateway != nil {
			return r.Gateway.Fetch(ctx, target, metric)
		}
	case "queueDepth", "queue":
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

func isCPUOrMemory(metricType string) bool {
	switch NormalizeMetricType(metricType) {
	case "cpu", "memory":
		return true
	default:
		return false
	}
}

// NormalizeMetricType maps aliases to canonical metric type names used by 24.03+.
func NormalizeMetricType(metricType string) string {
	switch strings.ToLower(strings.TrimSpace(metricType)) {
	case "cpu":
		return "cpu"
	case "memory":
		return "memory"
	case "custom":
		return "custom"
	case "httprequests", "http_requests", "requestrate", "request_rate", "rps":
		return "httpRequests"
	case "activeconnections", "active_connections", "connections":
		return "activeConnections"
	case "p95latency", "p95_latency", "latency":
		return "p95Latency"
	case "errorrate", "error_rate":
		return "errorRate"
	case "queuedepth", "queue_depth", "queue":
		return "queueDepth"
	default:
		return strings.TrimSpace(metricType)
	}
}

// IsTrafficRateMetric is true for per-replica request/connection targets.
func IsTrafficRateMetric(metricType string) bool {
	switch NormalizeMetricType(metricType) {
	case "httpRequests", "activeConnections":
		return true
	default:
		return false
	}
}

// IsGuardrailMetric is true for latency/error metrics that never scale down alone.
func IsGuardrailMetric(metricType string) bool {
	switch NormalizeMetricType(metricType) {
	case "p95Latency", "errorRate":
		return true
	default:
		return false
	}
}

// IsWorkloadUtilizationMetric is true for CPU/memory utilization signals.
func IsWorkloadUtilizationMetric(metricType string) bool {
	switch NormalizeMetricType(metricType) {
	case "cpu", "memory":
		return true
	default:
		return false
	}
}

// IsActuableMetric reports whether the evaluation loop should compute replica recommendations.
func IsActuableMetric(metricType string) bool {
	return IsWorkloadUtilizationMetric(metricType) ||
		IsTrafficRateMetric(metricType) ||
		IsGuardrailMetric(metricType)
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
