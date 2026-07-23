package evaluate

import (
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/schedule"
)

// OverrideState describes the resolved manual override for one tick.
type OverrideState struct {
	Active  bool
	Expired bool
	Value   *policy.ManualOverride
}

// ResolveOverride returns whether a manual override is still binding.
func ResolveOverride(status policy.ScalingPolicyStatus, now time.Time) OverrideState {
	ov := status.ManualOverride
	if ov == nil {
		return OverrideState{}
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(ov.ExpiresAt))
	if err != nil {
		// Invalid expiry → treat as expired so it clears.
		return OverrideState{Expired: true, Value: ov}
	}
	if !now.UTC().Before(expires.UTC()) {
		return OverrideState{Expired: true, Value: ov}
	}
	return OverrideState{Active: true, Value: ov}
}

// OutageDesired applies metric-outage fallback mode.
func OutageDesired(spec policy.ScalingPolicySpec, safeDesired, effectiveMin int) (desired int, mode string) {
	mode = policy.OutageHold
	if spec.MetricOutageFallback != nil && strings.TrimSpace(spec.MetricOutageFallback.Mode) != "" {
		mode = strings.ToLower(strings.TrimSpace(spec.MetricOutageFallback.Mode))
	}
	switch mode {
	case policy.OutageFloor:
		return effectiveMin, mode
	case policy.OutageFixed:
		if spec.MetricOutageFallback != nil && spec.MetricOutageFallback.FixedReplicas != nil {
			return *spec.MetricOutageFallback.FixedReplicas, mode
		}
		return safeDesired, policy.OutageHold
	default:
		return safeDesired, policy.OutageHold
	}
}

// FreezeActive reports whether scale-down must be blocked.
func FreezeActive(spec policy.ScalingPolicySpec, workloadProgressing bool, now time.Time) (frozen bool, reason string) {
	if workloadProgressing {
		return true, "WorkloadRollout"
	}
	if spec.DeploymentFreeze == nil || !spec.DeploymentFreeze.Enabled {
		return false, ""
	}
	until := strings.TrimSpace(spec.DeploymentFreeze.Until)
	if until == "" {
		return true, "DeploymentFreeze"
	}
	end, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return true, "DeploymentFreeze"
	}
	if now.UTC().Before(end.UTC()) {
		return true, "DeploymentFreezeWindow"
	}
	return false, ""
}

// ApplyScheduleBounds clamps desired into schedule-merged bounds.
func ApplyScheduleBounds(baseMin, baseMax int, schedules []policy.Schedule, desired int, now time.Time) (int, schedule.Bounds) {
	specs := make([]schedule.Spec, 0, len(schedules))
	for _, sch := range schedules {
		specs = append(specs, schedule.Spec{
			Name: sch.Name, Cron: sch.Cron, TimeZone: sch.TimeZone,
			MinReplicas: sch.MinReplicas, MaxReplicas: sch.MaxReplicas, EndTime: sch.EndTime,
		})
	}
	bounds := schedule.ActiveBounds(baseMin, baseMax, specs, now)
	return ClampReplicas(desired, bounds.Min, bounds.Max), bounds
}
