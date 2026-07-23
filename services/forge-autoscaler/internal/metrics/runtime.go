package metrics

import (
	"context"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// RuntimeSource is a degraded fallback path (wired in 24.02+).
type RuntimeSource struct {
	BaseURL string
}

// Fetch implements MetricSource (stub).
func (s *RuntimeSource) Fetch(_ context.Context, _ policy.TargetRef, _ policy.MetricSpec) (Sample, error) {
	return Sample{Source: "runtime"}, ErrNotImplemented
}
