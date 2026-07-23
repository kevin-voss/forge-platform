package evaluate

import (
	"fmt"
	"math"

	"forge.local/services/forge-autoscaler/internal/metrics"
)

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

// DesiredFromPerReplicaTarget computes ceil(totalMetric / targetPerReplica).
// Used for httpRequests / activeConnections where the policy target is per replica
// and the observed value is the application-wide total.
func DesiredFromPerReplicaTarget(totalMetric, targetPerReplica float64) int {
	if targetPerReplica <= 0 || totalMetric < 0 {
		return 0
	}
	if totalMetric == 0 {
		return 0
	}
	return int(math.Ceil(totalMetric/targetPerReplica - 1e-9))
}

// GuardrailRecommendation computes a scale-up-or-hold recommendation for latency/error metrics.
// These signals never recommend a replica count below currentReplicas.
func GuardrailRecommendation(currentReplicas int, observed, target float64, sampleCount, minSamples int64) (desired int, reasonCode string) {
	if minSamples <= 0 {
		minSamples = metrics.DefaultMinSampleCount
	}
	if sampleCount < minSamples {
		return currentReplicas, "HoldInsufficientSamples"
	}
	if target <= 0 {
		return currentReplicas, "HoldInvalidTarget"
	}
	if observed <= target {
		return currentReplicas, "HoldWithinTarget"
	}
	raw := DesiredFromUtilization(currentReplicas, observed, target)
	if raw <= currentReplicas {
		if currentReplicas < math.MaxInt {
			return currentReplicas + 1, "ScaleUpGuardrail"
		}
		return currentReplicas, "ScaleUpGuardrail"
	}
	return raw, "ScaleUpGuardrail"
}

// ReasonForTrafficRate builds a stable reason code/message for rate metrics.
func ReasonForTrafficRate(metricType string, current, recommended int, observed, target float64) string {
	code := "ScaleHoldTraffic"
	if recommended > current {
		code = "ScaleUpTraffic"
	} else if recommended < current {
		code = "ScaleDownTraffic"
	}
	return fmt.Sprintf("%s: ceil(%.4g / %.4g) = %d (metric=%s)", code, observed, target, recommended, metrics.NormalizeMetricType(metricType))
}

// DesiredFromQueueBacklog computes ceil(backlog / targetPerWorker).
// Identical to per-replica traffic math; named for worker/queue clarity.
func DesiredFromQueueBacklog(backlog, targetPerWorker float64) int {
	return DesiredFromPerReplicaTarget(backlog, targetPerWorker)
}

// QueuePressureRecommendation scales up when a queue SLO (age/lag/duration/DLQ) is breached.
// These signals never recommend below currentReplicas.
func QueuePressureRecommendation(currentReplicas int, observed, target float64) (desired int, reasonCode string) {
	if target <= 0 {
		return currentReplicas, "HoldInvalidTarget"
	}
	if observed <= target {
		return currentReplicas, "HoldWithinQueueSLO"
	}
	raw := DesiredFromUtilization(currentReplicas, observed, target)
	if raw <= currentReplicas {
		if currentReplicas < math.MaxInt {
			return currentReplicas + 1, "ScaleUpQueuePressure"
		}
		return currentReplicas, "ScaleUpQueuePressure"
	}
	return raw, "ScaleUpQueuePressure"
}

// RetryRateDecision returns whether retry pressure blocks scale-down and an optional scale-up.
// Any breach blocks scale-down. Mild breaches (below 2× target) hold; severe breaches may scale up.
func RetryRateDecision(currentReplicas int, observed, target float64) (desired int, blockScaleDown bool, reasonCode string) {
	if target <= 0 {
		return currentReplicas, false, "HoldInvalidTarget"
	}
	if observed <= target {
		return currentReplicas, false, "HoldRetryHealthy"
	}
	if observed < target*2 {
		return currentReplicas, true, "HoldRetryPressure"
	}
	raw := DesiredFromUtilization(currentReplicas, observed, target)
	if raw < currentReplicas {
		raw = currentReplicas
	}
	if raw == currentReplicas && currentReplicas < math.MaxInt {
		raw = currentReplicas + 1
	}
	return raw, true, "ScaleUpRetryPressure"
}

// ReasonForQueue builds a stable reason for queue backlog decisions.
func ReasonForQueue(metricType string, current, recommended int, observed, target float64) string {
	code := "ScaleHoldQueue"
	if recommended > current {
		code = "ScaleUpQueue"
	} else if recommended < current {
		code = "ScaleDownQueue"
	}
	return fmt.Sprintf("%s: backlog=%.4g targetPerWorker=%.4g → %d workers (metric=%s)",
		code, observed, target, recommended, metrics.NormalizeMetricType(metricType))
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
