package evaluate

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

// Store is the subset of policy.Store used by the evaluation loop.
type Store interface {
	ListAll(ctx context.Context) ([]policy.Row, error)
	ReplaceStatus(ctx context.Context, project, env, name string, expectedRV int64, status policy.ScalingPolicyStatus) (policy.Envelope, error)
}

// Loop ticks periodically, fetches metrics, and records recommendations (no actuation).
type Loop struct {
	Store    Store
	Source   metrics.MetricSource
	Interval time.Duration
	Log      *slog.Logger
}

// Run blocks until ctx is cancelled, evaluating on each tick.
func (l *Loop) Run(ctx context.Context) {
	interval := l.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Immediate first tick so short-interval tests / demos do not wait a full period.
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
			// Continue — one failing policy never blocks others.
		}
	}
}

func (l *Loop) evaluateOne(ctx context.Context, row policy.Row) error {
	status := row.Status
	status.ObservedGeneration = row.Generation
	fetchFailed := false
	now := time.Now().UTC().Format(time.RFC3339)

	for _, metric := range row.Spec.Metrics {
		sample, err := l.Source.Fetch(ctx, row.Spec.TargetRef, metric)
		target := metrics.TargetAverage(metric)
		rec := policy.Recommendation{
			MetricType:          metric.Type,
			TargetValue:         floatPtr(target),
			RecommendedReplicas: nil,
			ComputedAt:          now,
		}
		if err != nil {
			fetchFailed = true
			rec.MetricValue = nil
			rec.Reason = "metric fetch failed: " + err.Error()
			if errors.Is(err, metrics.ErrNotImplemented) {
				rec.Reason = "recorded, no actuation in 24.01; " + err.Error()
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
		} else {
			v := sample.Value
			rec.MetricValue = &v
			if sample.Target != 0 {
				rec.TargetValue = floatPtr(sample.Target)
			}
			rec.Reason = "recorded, no actuation in 24.01"
			if l.Log != nil {
				l.Log.Info("autoscaler evaluation",
					"policy_id", row.ID,
					"target_kind", row.Spec.TargetRef.Kind,
					"target_name", row.Spec.TargetRef.Name,
					"metric_type", metric.Type,
					"metric_value", sample.Value,
					"target_value", target,
					"reason", rec.Reason,
				)
			}
		}
		policy.AppendRecommendation(&status, rec)
	}

	policy.SetCondition(&status, policy.Condition{
		Type:   "AbleToScale",
		Status: "True",
		Reason: "ReadyForScaling",
	})
	if fetchFailed {
		policy.SetCondition(&status, policy.Condition{
			Type:   "ScalingActive",
			Status: "Unknown",
			Reason: "MetricFetchFailed",
		})
		status.Phase = "Degraded"
	} else {
		policy.SetCondition(&status, policy.Condition{
			Type:   "ScalingActive",
			Status: "True",
			Reason: "RecommendationsRecorded",
		})
		status.Phase = "Ready"
	}

	_, err := l.Store.ReplaceStatus(ctx, row.Project, row.Environment, row.Name, row.ResourceVersion, status)
	if err != nil && errors.Is(err, policy.ErrConflict) {
		// Lost the race — next tick will retry.
		return nil
	}
	return err
}

func floatPtr(v float64) *float64 { return &v }
