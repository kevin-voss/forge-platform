package metrics

import (
	"context"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// QueueSource will query Forge Events / Queue depth signals in 24.04+.
type QueueSource struct {
	BaseURL string
}

// Fetch implements MetricSource (stub).
func (s *QueueSource) Fetch(_ context.Context, _ policy.TargetRef, _ policy.MetricSpec) (Sample, error) {
	return Sample{Source: "queue"}, ErrNotImplemented
}
