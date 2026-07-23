package evaluate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
	"forge.local/services/forge-autoscaler/internal/audit"
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
	Store          Store
	Source         metrics.MetricSource
	Actuator       actuate.Actuator
	Stabilizer     *Stabilizer
	Metrics        *telemetry.Registry
	Events         audit.Publisher
	Interval       time.Duration
	Log            *slog.Logger
	Now            func() time.Time
	MinSampleCount int64
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

func (l *Loop) minSamples() int64 {
	if l.MinSampleCount > 0 {
		return l.MinSampleCount
	}
	return metrics.DefaultMinSampleCount
}

func (l *Loop) evaluateOne(ctx context.Context, row policy.Row) error {
	status := row.Status
	status.ObservedGeneration = row.Generation
	now := l.now()
	nowStr := now.Format(time.RFC3339)
	fetchFailed := false
	rawDesired := 0
	haveRecommendation := false
	blockScaleDown := false
	var dominant *policy.Recommendation

	currentReplicas := l.resolveCurrentReplicas(ctx, row)
	workloadProgressing := l.workloadProgressing(ctx, row)

	// --- Manual override expiry ---
	ovState := ResolveOverride(status, now)
	if ovState.Expired && ovState.Value != nil {
		status.ManualOverride = nil
		policy.AppendAudit(&status, policy.AuditEntry{
			Type:    "override.expired",
			At:      nowStr,
			Message: "manual override TTL elapsed",
			Actor:   ovState.Value.CreatedBy,
		})
		l.publish(ctx, audit.NewEvent(audit.OverrideExpired, row.Project, row.Environment, row.Name, map[string]any{
			"reason":     "ttl_elapsed",
			"expires_at": ovState.Value.ExpiresAt,
		}, now))
		ovState = OverrideState{}
	}

	// --- Schedules ---
	prevActive := map[string]struct{}{}
	for _, name := range status.ActiveSchedules {
		prevActive[name] = struct{}{}
	}
	_, bounds := ApplyScheduleBounds(row.Spec.MinReplicas, row.Spec.MaxReplicas, row.Spec.Schedules, currentReplicas, now)
	status.ActiveSchedules = bounds.Active
	status.EffectiveMinReplicas = intPtr(bounds.Min)
	status.EffectiveMaxReplicas = intPtr(bounds.Max)
	if bounds.Conflict {
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScheduleConflict",
			Status:  "True",
			Reason:  "ConflictingSchedules",
			Message: bounds.ConflictMessage,
		})
	} else {
		policy.SetCondition(&status, policy.Condition{
			Type:   "ScheduleConflict",
			Status: "False",
			Reason: "NoConflict",
		})
	}
	for _, name := range bounds.Active {
		if _, seen := prevActive[name]; !seen {
			policy.AppendAudit(&status, policy.AuditEntry{
				Type:     "schedule.activated",
				At:       nowStr,
				Message:  "schedule became active",
				Schedule: name,
			})
			l.publish(ctx, audit.NewEvent(audit.ScheduleActive, row.Project, row.Environment, row.Name, map[string]any{
				"schedule": name,
				"min":      bounds.Min,
				"max":      bounds.Max,
			}, now))
		}
	}
	if l.Metrics != nil {
		l.Metrics.SetScheduleActive(row.Name, len(bounds.Active) > 0)
		l.Metrics.SetManualOverrideActive(row.Name, ovState.Active)
	}

	// Manual override supersedes metrics entirely.
	if ovState.Active && ovState.Value != nil {
		desired := ovState.Value.Replicas
		// Still honour absolute policy max as a hard safety ceiling.
		if desired > row.Spec.MaxReplicas {
			desired = row.Spec.MaxReplicas
		}
		if desired < 0 {
			desired = 0
		}
		status.CurrentReplicas = currentReplicas
		status.DesiredReplicas = desired
		status.MetricOutageMode = ""
		frozen, freezeReason := FreezeActive(row.Spec, workloadProgressing, now)
		status.DeploymentFrozen = frozen
		// Override wins even during freeze (operator intent), but record freeze status.
		policy.SetCondition(&status, policy.Condition{
			Type:    "AbleToScale",
			Status:  "True",
			Reason:  "ManualOverride",
			Message: ovState.Value.Reason,
		})
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "True",
			Reason:  "ManualOverride",
			Message: fmt.Sprintf("override replicas=%d until %s", desired, ovState.Value.ExpiresAt),
		})
		status.Phase = "Ready"
		if frozen {
			policy.SetCondition(&status, policy.Condition{
				Type:    "DeploymentFreeze",
				Status:  "True",
				Reason:  freezeReason,
				Message: "freeze active; manual override still applied",
			})
		} else {
			policy.SetCondition(&status, policy.Condition{
				Type:   "DeploymentFreeze",
				Status: "False",
				Reason: "NotFrozen",
			})
		}
		return l.actuateAndPersist(ctx, row, &status, currentReplicas, desired, row.Status.DesiredReplicas, now, nowStr)
	}

	for _, metric := range row.Spec.Metrics {
		if !metrics.IsActuableMetric(metric.Type) {
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

		if metrics.IsTrafficRateMetric(metric.Type) {
			if l.Log != nil {
				l.Log.Info("autoscaler metric fetch",
					"span", "autoscaler.metrics.gateway",
					"policy_id", row.ID,
					"metric_type", metric.Type,
					"target_name", row.Spec.TargetRef.Name,
				)
			}
		}
		if metrics.IsQueueMetric(metric.Type) {
			if l.Log != nil {
				l.Log.Info("autoscaler metric fetch",
					"span", "autoscaler.metrics.queue",
					"policy_id", row.ID,
					"metric_type", metric.Type,
					"target_name", row.Spec.TargetRef.Name,
					"queue", metrics.QueueName(row.Spec.TargetRef, metric),
				)
			}
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
			if errors.Is(err, metrics.ErrUnavailable) {
				rec.Reason = "MetricUnavailable: " + err.Error()
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

		recommended, reason, blockDown := l.recommend(currentReplicas, metric.Type, v, target, sample.SampleCount)
		if blockDown {
			blockScaleDown = true
		}
		recommended = ClampReplicas(recommended, bounds.Min, bounds.Max)
		rec.RecommendedReplicas = intPtr(recommended)
		rec.Reason = reason
		if l.Log != nil {
			l.Log.Info("autoscaler evaluation",
				"policy_id", row.ID,
				"target_kind", row.Spec.TargetRef.Kind,
				"target_name", row.Spec.TargetRef.Name,
				"metric_type", metric.Type,
				"metric_value", v,
				"observed", v,
				"target", target,
				"target_value", target,
				"recommended_replicas", recommended,
				"sample_count", sample.SampleCount,
				"source", sample.Source,
				"queue", sample.QueueName,
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
	if safeDesired < bounds.Min {
		safeDesired = bounds.Min
	}

	policy.SetCondition(&status, policy.Condition{
		Type:   "AbleToScale",
		Status: "True",
		Reason: "ReadyForScaling",
	})

	frozen, freezeReason := FreezeActive(row.Spec, workloadProgressing, now)
	status.DeploymentFrozen = frozen
	if frozen {
		policy.SetCondition(&status, policy.Condition{
			Type:    "DeploymentFreeze",
			Status:  "True",
			Reason:  freezeReason,
			Message: "scale-down blocked; scale-up allowed",
		})
	} else {
		policy.SetCondition(&status, policy.Condition{
			Type:   "DeploymentFreeze",
			Status: "False",
			Reason: "NotFrozen",
		})
	}

	if fetchFailed && !haveRecommendation {
		desired, mode := OutageDesired(row.Spec, safeDesired, bounds.Min)
		desired = ClampReplicas(desired, bounds.Min, bounds.Max)
		if frozen && desired < currentReplicas {
			desired = currentReplicas
		}
		status.DesiredReplicas = desired
		status.MetricOutageMode = mode
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "Unknown",
			Reason:  "MetricFetchFailed",
			Message: fmt.Sprintf("metric outage fallback mode=%s", mode),
		})
		status.Phase = "Degraded"
		if dominant != nil {
			status.LastRecommendation = dominant
		}
		return l.persistStatus(ctx, row, status)
	}

	status.MetricOutageMode = ""

	if !haveRecommendation {
		desired := ClampReplicas(safeDesired, bounds.Min, bounds.Max)
		if frozen && desired < currentReplicas {
			desired = currentReplicas
		}
		status.DesiredReplicas = desired
		policy.SetCondition(&status, policy.Condition{
			Type:   "ScalingActive",
			Status: "True",
			Reason: "NoActuableMetrics",
		})
		status.Phase = "Ready"
		return l.persistStatus(ctx, row, status)
	}

	if fetchFailed {
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "Unknown",
			Reason:  "MetricFetchFailed",
			Message: "partial metric failure; using successful signals",
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
	desired = ClampReplicas(desired, bounds.Min, bounds.Max)

	if blockScaleDown {
		if desired < currentReplicas {
			desired = currentReplicas
		}
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "True",
			Reason:  "RetryPressureBlocksScaleDown",
			Message: "retry-rate spike blocks scale-down until pressure recovers",
		})
	}

	if frozen && desired < currentReplicas {
		desired = currentReplicas
		policy.SetCondition(&status, policy.Condition{
			Type:    "ScalingActive",
			Status:  "True",
			Reason:  freezeReason,
			Message: "deployment freeze blocked scale-down",
		})
	}

	if l.Metrics != nil {
		l.Metrics.SetRecommendationReplicas(row.Name, row.Spec.TargetRef.Kind, row.Spec.TargetRef.Name, desired)
		if strings.EqualFold(row.Spec.TargetRef.Kind, "Worker") {
			l.Metrics.SetWorkerDesiredReplicas(row.Spec.TargetRef.Name, desired)
		}
	}
	if dominant != nil {
		status.LastRecommendation = dominant
		if l.Log != nil {
			observed := 0.0
			if dominant.MetricValue != nil {
				observed = *dominant.MetricValue
			}
			target := 0.0
			if dominant.TargetValue != nil {
				target = *dominant.TargetValue
			}
			recReplicas := desired
			if dominant.RecommendedReplicas != nil {
				recReplicas = *dominant.RecommendedReplicas
			}
			l.Log.Info("autoscaler dominant metric",
				"policy_id", row.ID,
				"metric_type", dominant.MetricType,
				"observed", observed,
				"target", target,
				"recommended_replicas", recReplicas,
				"reason", dominant.Reason,
			)
		}
	}
	status.DesiredReplicas = desired

	return l.actuateAndPersist(ctx, row, &status, currentReplicas, desired, row.Status.DesiredReplicas, now, nowStr)
}

func (l *Loop) actuateAndPersist(
	ctx context.Context,
	row policy.Row,
	status *policy.ScalingPolicyStatus,
	currentReplicas, desired, previousDesired int,
	now time.Time,
	nowStr string,
) error {
	safeDesired := previousDesired
	if safeDesired < row.Spec.MinReplicas {
		safeDesired = row.Spec.MinReplicas
	}

	if desired != currentReplicas && l.Actuator != nil && isActuableTarget(row.Spec.TargetRef.Kind) {
		opID := fmt.Sprintf("scale-%s-%d", row.ID, now.UnixNano())
		direction := "up"
		if desired < currentReplicas {
			direction = "down"
		}
		actCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		span := "autoscaler.actuate.application"
		if strings.EqualFold(row.Spec.TargetRef.Kind, "Worker") {
			span = "autoscaler.actuate.worker"
		}
		if l.Log != nil {
			l.Log.Info("autoscaler actuate",
				"span", span,
				"policy_id", row.ID,
				"target_kind", row.Spec.TargetRef.Kind,
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
			row.Spec.TargetRef.Kind,
			row.Spec.TargetRef.Name,
			desired,
			opID,
		)
		if err != nil {
			if l.Metrics != nil {
				l.Metrics.IncScaleAction(direction, "error")
			}
			policy.SetCondition(status, policy.Condition{
				Type:    "AbleToScale",
				Status:  "False",
				Reason:  "ActuationFailed",
				Message: err.Error(),
			})
			status.Phase = "Degraded"
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
		status.DesiredReplicas = desired
		status.LastScaleTime = nowStr
	}

	return l.persistStatus(ctx, row, *status)
}

func (l *Loop) persistStatus(ctx context.Context, row policy.Row, status policy.ScalingPolicyStatus) error {
	_, err := l.Store.ReplaceStatus(ctx, row.Project, row.Environment, row.Name, row.ResourceVersion, status)
	if err != nil && errors.Is(err, policy.ErrConflict) {
		return nil
	}
	return err
}

func (l *Loop) publish(ctx context.Context, ev audit.Event) {
	if l.Events == nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := l.Events.Publish(pubCtx, ev); err != nil && l.Log != nil {
		l.Log.Warn("audit event publish failed", "type", ev.Type, "error", err.Error())
	}
}

func (l *Loop) recommend(currentReplicas int, metricType string, observed, target float64, sampleCount int64) (desired int, reason string, blockScaleDown bool) {
	switch {
	case metrics.IsWorkloadUtilizationMetric(metricType):
		recommended := DesiredFromUtilization(currentReplicas, observed, target)
		reason := fmt.Sprintf("ScaleUtilization: ceil(%d * %.4g / %.4g) = %d", currentReplicas, observed, target, recommended)
		return recommended, reason, false
	case metrics.IsTrafficRateMetric(metricType):
		recommended := DesiredFromPerReplicaTarget(observed, target)
		if recommended == 0 && currentReplicas > 0 && observed == 0 {
			recommended = 0
		}
		return recommended, ReasonForTrafficRate(metricType, currentReplicas, recommended, observed, target), false
	case metrics.IsGuardrailMetric(metricType):
		recommended, code := GuardrailRecommendation(currentReplicas, observed, target, sampleCount, l.minSamples())
		return recommended, fmt.Sprintf("%s: observed=%.4g target=%.4g samples=%d", code, observed, target, sampleCount), false
	case metrics.IsQueueDepthMetric(metricType):
		recommended := DesiredFromQueueBacklog(observed, target)
		return recommended, ReasonForQueue(metricType, currentReplicas, recommended, observed, target), false
	case metrics.IsQueuePressureMetric(metricType):
		recommended, code := QueuePressureRecommendation(currentReplicas, observed, target)
		return recommended, fmt.Sprintf("%s: observed=%.4g target=%.4g (metric=%s)", code, observed, target, metrics.NormalizeMetricType(metricType)), false
	case metrics.IsRetryRateMetric(metricType):
		recommended, block, code := RetryRateDecision(currentReplicas, observed, target)
		return recommended, fmt.Sprintf("%s: observed=%.4g target=%.4g", code, observed, target), block
	default:
		return currentReplicas, "HoldUnknownMetric", false
	}
}

func (l *Loop) resolveCurrentReplicas(ctx context.Context, row policy.Row) int {
	if l.Actuator != nil && isActuableTarget(row.Spec.TargetRef.Kind) {
		view, err := l.Actuator.Get(ctx, row.Project, row.Environment, row.Spec.TargetRef.Kind, row.Spec.TargetRef.Name)
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

func (l *Loop) workloadProgressing(ctx context.Context, row policy.Row) bool {
	if l.Actuator == nil || !isActuableTarget(row.Spec.TargetRef.Kind) {
		return false
	}
	view, err := l.Actuator.Get(ctx, row.Project, row.Environment, row.Spec.TargetRef.Kind, row.Spec.TargetRef.Name)
	if err != nil {
		return false
	}
	return view.Progressing
}

func isActuableTarget(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "application", "worker":
		return true
	default:
		return false
	}
}

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }
