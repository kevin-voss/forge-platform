package metrics

import (
	"context"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// GatewaySource will query Forge Gateway admin metrics in 24.03+.
type GatewaySource struct {
	BaseURL string
}

// Fetch implements MetricSource (stub).
func (s *GatewaySource) Fetch(_ context.Context, _ policy.TargetRef, _ policy.MetricSpec) (Sample, error) {
	return Sample{Source: "gateway"}, ErrNotImplemented
}
