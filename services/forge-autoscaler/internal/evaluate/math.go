package evaluate

import "math"

// DesiredFromUtilization computes ceil(currentReplicas * currentMetric / targetMetric).
// When currentReplicas is 0 and utilization exceeds the target, returns 1 so scale-from-zero
// can start (scale-to-zero itself remains out of scope for policies with minReplicas >= 1).
func DesiredFromUtilization(currentReplicas int, currentMetric, targetMetric float64) int {
	if targetMetric <= 0 || currentMetric < 0 {
		return currentReplicas
	}
	if currentReplicas <= 0 {
		if currentMetric > targetMetric {
			return 1
		}
		return 0
	}
	raw := float64(currentReplicas) * currentMetric / targetMetric
	return int(math.Ceil(raw - 1e-9))
}

// ClampReplicas bounds n to [minReplicas, maxReplicas].
func ClampReplicas(n, minReplicas, maxReplicas int) int {
	if minReplicas < 0 {
		minReplicas = 0
	}
	if maxReplicas < minReplicas {
		maxReplicas = minReplicas
	}
	if n < minReplicas {
		return minReplicas
	}
	if n > maxReplicas {
		return maxReplicas
	}
	return n
}

// LimitReplicaDelta caps how far desired may move from current in one evaluation,
// using maxReplicasPerMinute as the absolute replica delta budget.
// A non-positive maxReplicasPerMinute means unlimited.
func LimitReplicaDelta(current, desired, maxReplicasPerMinute int) int {
	if maxReplicasPerMinute <= 0 || current == desired {
		return desired
	}
	delta := desired - current
	if delta > maxReplicasPerMinute {
		return current + maxReplicasPerMinute
	}
	if delta < -maxReplicasPerMinute {
		return current - maxReplicasPerMinute
	}
	return desired
}
