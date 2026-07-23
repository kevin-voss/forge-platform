package metrics

import (
	"context"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// ObserveSource will query Forge Observe (PromQL) in 24.02+.
type ObserveSource struct {
	BaseURL string
}

// Fetch implements MetricSource (stub).
func (s *ObserveSource) Fetch(_ context.Context, _ policy.TargetRef, _ policy.MetricSpec) (Sample, error) {
	return Sample{Source: "observe"}, ErrNotImplemented
}
