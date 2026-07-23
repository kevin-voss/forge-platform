package evaluate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

// Store is the subset of policy.Store used by the evaluation loop.
type Store interface {
	ListAll(ctx context.Context) ([]policy.Row, error)
	ReplaceStatus(ctx context.Context, project, env, name string, expectedRV int64, status policy.ScalingPolicyStatus) (policy.Envelope, error)
}

// Loop ticks periodically, fetches metrics, computes desired replicas, and actuates.
type Loop struct {
	Store      Store
	Source     metrics.MetricSource
	Actuator   actuate.Actuator
	Stabilizer *Stabilizer
	Metrics    *telemetry.Registry
	Interval   time.Duration
	Log        *slog.Logger
	Now        func() time.Time
}

// Run blocks until ctx is cancelled, evaluating on each tick.
func (l *Loop) Run(ctx context.Context) {
	interval := l.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	l.Tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Tick(ctx)
		}
	}
}

// Tick evaluates every ScalingPolicy once.
func (l *Loop) Tick(ctx context.Context) {
	rows, err := l.Store.ListAll(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Error("list scaling policies failed", "error", err.Error())
		}
		return
	}
	for _, row := range rows {
		if err := l.evaluateOne(ctx, row); err != nil {
			if l.Log != nil {
				l.Log.Error("evaluate policy failed",
					"policy_id", row.ID,
					"error", err.Error(),
				)
			}
		}
	}
}

func (l *Loop) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

func (l *Loop) evaluateOne(ctx context.Context, row policy.Row) error {
	status := row.Status
	status.ObservedGeneration = row.Generation
	now := l.now()
	nowStr := now.Format(time.RFC3339)
	fetchFailed := false
	rawDesired := 0
	haveRecommendation := false
	var dominant *policy.Recommendation

	currentReplicas := l.resolveCurrentReplicas(ctx, row)

	for _, metric := range row.Spec.Metrics {
		if !isWorkloadUtilizationMetric(metric.Type) {
			// Later steps (24.03+) own non-utilization metrics; skip actuation math here.
			sample, err := l.Source.Fetch(ctx, row.Spec.TargetRef, metric)
			rec := policy.Recommendation{
				MetricType:  metric.Type,
				TargetValue: floatPtr(metrics.TargetAverage(metric)),
				ComputedAt:  nowStr,
			}
			if err != nil {
				fetchFailed = true
				rec.Reason = "metric fetch failed: " + err.Error()
			} else {
				v := sample.Value
				rec.MetricValue = &v
				rec.Reason = "recorded; actuation deferred to later metric step"
			}
			policy.AppendRecommendation(&status, rec)
			continue
		}

		sample, err := l.Source.Fetch(ctx, row.Spec.TargetRef, metric)
		target := metrics.TargetAverage(metric)
		rec := policy.Recommendation{
			MetricType:  metric.Type,
			TargetValue: floatPtr(target),
			ComputedAt:  nowStr,
		}
		if err != nil {
			fetchFailed = true
			rec.MetricValue = nil
			rec.Reason = "metric fetch failed: " + err.Error()
			if errors.Is(err, metrics.ErrNotImplemented) {
				rec.Reason = "metric fetch failed: " + err.Error()
			}
			if l.Log != nil {
				l.Log.Info("autoscaler evaluation",
					"policy_id", row.ID,
					"target_kind", row.Spec.TargetRef.Kind,
					"target_name", row.Spec.TargetRef.Name,
					"metric_type", metric.Type,
					"reason", rec.Reason,
				)
			}
			policy.AppendRecommendation(&status, rec)
			continue
		}

		v := sample.Value
		rec.MetricValue = &v
		if sample.Target != 0 {
			rec.TargetValue = floatPtr(sample.Target)
			target = sample.Target
		}
		recommended := DesiredFromUtilization(currentReplicas, v, target)
		recommended = ClampReplicas(recommended, row.Spec.MinReplicas, row.Spec.MaxReplicas)
		rec.RecommendedReplicas = intPtr(recommended)
		rec.Reason = fmt.Sprintf("utilization math: ceil(%d * %.4g / %.4g) = %d", currentReplicas, v, target, recommended)
		if l.Log != nil {
			l.Log.Info("autoscaler evaluation",
				"policy_id", row.ID,
				"target_kind", row.Spec.TargetRef.Kind,
				"target_name", row.Spec.TargetRef.Name,
				"metric_type", metric.Type,
				"metric_value", v,
				"target_value", target,
				"recommended_replicas", recommended,
				"reason", rec.Reason,
			)
		}
		policy.AppendRecommendation(&status, rec)
		if !haveRecommendation || recommended > rawDesired {
			rawDesired = recommended
			haveRecommendation = true
			copy := rec
			dominant = &copy
		}
	}

	status.CurrentReplicas = currentReplicas
	safeDesired := status.DesiredReplicas
	if safeDesired < row.Spec.MinReplicas {
		safeDesired = row.Spec.MinReplicas
	}

	policy.SetCondition(&status, policy.Condition{
		Type:   "AbleToScale",
		Status: "True",
		Reason: "ReadyForScaling",
	})

	if fetchFailed && !haveRecommendation {
		// Metric outage: hold last safe desired; never reduce below minReplicas.
		status.DesiredReplicas = safeDesired
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "Unknown",
			Reason:  "MetricFetchFailed",
			Message: "holding last safe desired replica count",
		})
		status.Phase = "Degraded"
		if dominant != nil {
			status.LastRecommendation = dominant
		}
		_, err := l.Store.ReplaceStatus(ctx, row.Project, row.Environment, row.Name, row.ResourceVersion, status)
		if err != nil && errors.Is(err, policy.ErrConflict) {
			return nil
		}
		return err
	}

	if !haveRecommendation {
		status.DesiredReplicas = safeDesired
		policy.SetCondition(&status, policy.Condition{
			Type:   "ScalingActive",
			Status: "True",
			Reason: "NoActuableMetrics",
		})
		status.Phase = "Ready"
		_, err := l.Store.ReplaceStatus(ctx, row.Project, row.Environment, row.Name, row.ResourceVersion, status)
		if err != nil && errors.Is(err, policy.ErrConflict) {
			return nil
		}
		return err
	}

	if fetchFailed {
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "Unknown",
			Reason:  "MetricFetchFailed",
			Message: "partial metric failure; using successful utilization signals",
		})
		status.Phase = "Degraded"
	} else {
		policy.SetCondition(&status, policy.Condition{
			Type:   "ScalingActive",
			Status: "True",
			Reason: "ScalingActive",
		})
		status.Phase = "Ready"
	}

	stabilizer := l.Stabilizer
	if stabilizer == nil {
		stabilizer = NewStabilizer()
		l.Stabilizer = stabilizer
	}
	policyKey := row.Project + "/" + row.Environment + "/" + row.Name
	stabilized := stabilizer.Apply(
		policyKey,
		currentReplicas,
		rawDesired,
		time.Duration(row.Spec.Behavior.ScaleUp.StabilizationWindowSeconds)*time.Second,
		time.Duration(row.Spec.Behavior.ScaleDown.StabilizationWindowSeconds)*time.Second,
		now,
	)

	rateLimit := row.Spec.Behavior.ScaleUp.MaxReplicasPerMinute
	if stabilized < currentReplicas {
		rateLimit = row.Spec.Behavior.ScaleDown.MaxReplicasPerMinute
	}
	desired := LimitReplicaDelta(currentReplicas, stabilized, rateLimit)
	desired = ClampReplicas(desired, row.Spec.MinReplicas, row.Spec.MaxReplicas)

	if l.Metrics != nil {
		l.Metrics.SetRecommendationReplicas(row.Name, row.Spec.TargetRef.Kind, row.Spec.TargetRef.Name, desired)
	}
	if dominant != nil {
		status.LastRecommendation = dominant
	}
	status.DesiredReplicas = desired

	if desired != currentReplicas && l.Actuator != nil &&
		strings.EqualFold(row.Spec.TargetRef.Kind, "Application") {
		opID := fmt.Sprintf("scale-%s-%d", row.ID, now.UnixNano())
		direction := "up"
		if desired < currentReplicas {
			direction = "down"
		}
		actCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if l.Log != nil {
			l.Log.Info("autoscaler actuate",
				"span", "autoscaler.actuate.application",
				"policy_id", row.ID,
				"target_name", row.Spec.TargetRef.Name,
				"from", currentReplicas,
				"to", desired,
				"operation_id", opID,
			)
		}
		_, err := l.Actuator.SetDesiredReplicas(
			actCtx,
			row.Project,
			row.Environment,
			row.Spec.TargetRef.Name,
			desired,
			opID,
		)
		if err != nil {
			if l.Metrics != nil {
				l.Metrics.IncScaleAction(direction, "error")
			}
			policy.SetCondition(&status, policy.Condition{
				Type:    "AbleToScale",
				Status:  "False",
				Reason:  "ActuationFailed",
				Message: err.Error(),
			})
			status.Phase = "Degraded"
			// Keep previous safe desired on actuation failure.
			status.DesiredReplicas = safeDesired
		} else {
			if l.Metrics != nil {
				l.Metrics.IncScaleAction(direction, "success")
			}
			status.LastScaleTime = nowStr
			status.CurrentReplicas = desired
			status.DesiredReplicas = desired
		}
	} else if desired == currentReplicas {
		status.DesiredReplicas = desired
	} else if l.Actuator == nil {
		// Dry-run / tests without actuator still record the decision.
		status.DesiredReplicas = desired
		status.LastScaleTime = nowStr
	}

	_, err := l.Store.ReplaceStatus(ctx, row.Project, row.Environment, row.Name, row.ResourceVersion, status)
	if err != nil && errors.Is(err, policy.ErrConflict) {
		return nil
	}
	return err
}

func (l *Loop) resolveCurrentReplicas(ctx context.Context, row policy.Row) int {
	if l.Actuator != nil && strings.EqualFold(row.Spec.TargetRef.Kind, "Application") {
		view, err := l.Actuator.Get(ctx, row.Project, row.Environment, row.Spec.TargetRef.Name)
		if err == nil && view.HasDesired {
			return view.DesiredReplicas
		}
	}
	if row.Status.DesiredReplicas > 0 {
		return row.Status.DesiredReplicas
	}
	if row.Status.CurrentReplicas > 0 {
		return row.Status.CurrentReplicas
	}
	return row.Spec.MinReplicas
}

func isWorkloadUtilizationMetric(metricType string) bool {
	switch strings.ToLower(strings.TrimSpace(metricType)) {
	case "cpu", "memory":
		return true
	default:
		return false
	}
}

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
