package evaluate

import (
	"sync"
	"time"
)

// recommendationSample is one raw replica recommendation used for stabilization.
type recommendationSample struct {
	replicas   int
	observedAt time.Time
}

// Stabilizer remembers recent recommendations per policy and applies
// scale-up / scale-down stabilization windows (Kubernetes HPA-style):
//   - scale-up: pick the highest recommendation inside the window
//   - scale-down: pick the highest recommendation inside the window
//     (most conservative — prevents rapid downscaling)
type Stabilizer struct {
	mu   sync.Mutex
	hist map[string][]recommendationSample
}

// NewStabilizer creates an empty stabilizer.
func NewStabilizer() *Stabilizer {
	return &Stabilizer{hist: map[string][]recommendationSample{}}
}

// Apply records rawDesired and returns the stabilized replica count relative to current.
func (s *Stabilizer) Apply(
	policyKey string,
	current, rawDesired int,
	scaleUpWindow, scaleDownWindow time.Duration,
	now time.Time,
) int {
	if s == nil {
		return rawDesired
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	samples := append(s.hist[policyKey], recommendationSample{replicas: rawDesired, observedAt: now})
	keep := scaleUpWindow
	if scaleDownWindow > keep {
		keep = scaleDownWindow
	}
	if keep <= 0 {
		keep = time.Minute
	}
	cutoff := now.Add(-keep - time.Minute)
	trimmed := samples[:0]
	for _, sample := range samples {
		if !sample.observedAt.Before(cutoff) {
			trimmed = append(trimmed, sample)
		}
	}
	s.hist[policyKey] = trimmed

	if rawDesired == current {
		return current
	}
	window := scaleUpWindow
	if rawDesired < current {
		window = scaleDownWindow
	}
	return maxInWindow(trimmed, now, window, rawDesired)
}

func maxInWindow(samples []recommendationSample, now time.Time, window time.Duration, fallback int) int {
	if window <= 0 {
		return fallback
	}
	cutoff := now.Add(-window)
	max := fallback
	for _, sample := range samples {
		if sample.observedAt.Before(cutoff) {
			continue
		}
		if sample.replicas > max {
			max = sample.replicas
		}
	}
	return max
}
