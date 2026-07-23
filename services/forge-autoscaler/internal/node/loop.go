package node

import (
	"context"
	"log/slog"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
	"forge.local/services/forge-autoscaler/internal/audit"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

// DemandReader loads pending + reservation + fleet signals.
type DemandReader interface {
	ListPending(ctx context.Context) ([]PendingWorkload, error)
	ClusterReservation(ctx context.Context) (ClusterReservation, error)
	ListFleetNodes(ctx context.Context) ([]FleetNode, error)
}

// Loop periodically evaluates node scale-up and scale-down.
type Loop struct {
	Signals              DemandReader
	Pools                actuate.NodePoolActuator
	Guards               ScaleDownGuards
	Metrics              *telemetry.Registry
	Events               audit.Publisher
	Interval             time.Duration
	Cooldown             time.Duration
	ScaleDownCooldown    time.Duration
	UnderutilThreshold   float64
	UnderutilWindow      time.Duration
	MaxDeletesPerWindow  int
	ReservationThreshold float64
	DefaultMaxNodes      int
	ScaleDownEnabled     bool
	RetryUncordonOnBlock bool
	Log                  *slog.Logger
	Now                  func() time.Time
}

// Run blocks until ctx is cancelled.
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

func (l *Loop) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

// Tick runs scale-up then scale-down evaluation.
func (l *Loop) Tick(ctx context.Context) {
	l.tickScaleUp(ctx)
	if l.ScaleDownEnabled {
		l.tickScaleDown(ctx)
	}
}

func (l *Loop) tickScaleUp(ctx context.Context) {
	if l.Log != nil {
		l.Log.Info("node scale-up tick", "span", "autoscaler.node.scale_up")
	}
	pending, err := l.Signals.ListPending(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Warn("list pending workloads failed", "error", err.Error(), "span", "autoscaler.node.scale_up")
		}
		return
	}
	if l.Metrics != nil {
		l.Metrics.SetPendingWorkloads(len(pending))
	}

	reservation, err := l.Signals.ClusterReservation(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Warn("cluster reservation read failed", "error", err.Error(), "span", "autoscaler.node.scale_up")
		}
		reservation = ClusterReservation{}
	}

	pools, err := l.Pools.List(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Warn("list nodepools failed", "error", err.Error(), "span", "autoscaler.node.scale_up")
		}
		return
	}

	decision := EvaluateScaleUp(ScaleUpInput{
		Pending:              pending,
		Reservation:          reservation,
		Pools:                pools,
		ReservationThreshold: l.ReservationThreshold,
		Cooldown:             l.Cooldown,
		Now:                  l.now(),
		DefaultMaxNodes:      l.DefaultMaxNodes,
	})

	if decision.Condition == "NoEligibleNodePool" && len(pending) > 0 {
		l.recordScaleUpResult("", "no_eligible")
		l.publishScaleUp(ctx, decision)
		if l.Log != nil {
			l.Log.Info("no eligible nodepool for pending workloads",
				"span", "autoscaler.node.scale_up",
				"pending", len(pending),
				"condition", "NoEligibleNodePool",
			)
		}
		return
	}

	if err := ApplyScaleUp(ctx, l.Pools, decision, l.now()); err != nil {
		l.recordScaleUpResult(decision.PoolName, "error")
		if l.Log != nil {
			l.Log.Warn("node scale-up apply failed",
				"span", "autoscaler.node.scale_up",
				"pool", decision.PoolName,
				"action", decision.Action,
				"error", err.Error(),
			)
		}
		return
	}

	result := decision.Action
	if result == "scale_up" {
		result = "ok"
	}
	l.recordScaleUpResult(decision.PoolName, result)
	l.publishScaleUp(ctx, decision)
	if l.Log != nil {
		l.Log.Info("node scale-up evaluated",
			"span", "autoscaler.node.scale_up",
			"action", decision.Action,
			"pool", decision.PoolName,
			"desired_nodes", decision.DesiredNodes,
			"ready_nodes", decision.ReadyNodes,
			"pending", decision.PendingCount,
			"operation_id", decision.OperationID,
			"reason", decision.Reason,
		)
	}
}

func (l *Loop) tickScaleDown(ctx context.Context) {
	if l.Log != nil {
		l.Log.Info("node scale-down tick", "span", "autoscaler.node.scale_down")
	}

	pending, err := l.Signals.ListPending(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Warn("list pending workloads failed", "error", err.Error(), "span", "autoscaler.node.scale_down")
		}
		pending = nil
	}

	fleet, err := l.Signals.ListFleetNodes(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Warn("list fleet nodes failed", "error", err.Error(), "span", "autoscaler.node.scale_down")
		}
		return
	}

	pools, err := l.Pools.List(ctx)
	if err != nil {
		if l.Log != nil {
			l.Log.Warn("list nodepools failed", "error", err.Error(), "span", "autoscaler.node.scale_down")
		}
		return
	}

	in := ScaleDownInput{
		Pending:              pending,
		Fleet:                fleet,
		Pools:                pools,
		Cooldown:             l.ScaleDownCooldown,
		UnderutilThreshold:   l.UnderutilThreshold,
		UnderutilWindow:      l.UnderutilWindow,
		MaxDeletesPerWindow:  l.MaxDeletesPerWindow,
		Now:                  l.now(),
		RetryUncordonOnBlock: l.RetryUncordonOnBlock,
		StatefulBlocked:      map[string]bool{},
	}

	if l.Guards != nil {
		if active, reason, gerr := l.Guards.HasActiveDeployment(ctx); gerr != nil {
			if l.Log != nil {
				l.Log.Warn("active deployment guard failed", "error", gerr.Error(), "span", "autoscaler.node.scale_down")
			}
		} else if active {
			in.ActiveDeployment = true
			in.ActiveDeploymentReason = reason
		}
		// Probe disruption budget with empty hint (cluster soft check) — per-deployment
		// checks happen via StaticGuards in tests; HTTP path allows when API missing.
		if allow, reason, gerr := l.Guards.DisruptionBudgetAllows(ctx, ""); gerr != nil {
			if l.Log != nil {
				l.Log.Warn("disruption budget guard failed", "error", gerr.Error(), "span", "autoscaler.node.scale_down")
			}
		} else if !allow {
			in.DisruptionBudgetBlocked = true
			in.DisruptionBudgetReason = reason
		}
		for _, n := range fleet {
			if blocked, reason, gerr := l.Guards.HasStatefulPrimary(ctx, n); gerr != nil {
				continue
			} else if blocked {
				in.StatefulBlocked[n.ID] = true
				if in.StatefulReason == "" {
					in.StatefulReason = reason
				}
			}
		}
	}

	decision := EvaluateScaleDown(in)
	if l.Metrics != nil && decision.CandidateCount > 0 {
		l.Metrics.AddScaleDownCandidates(decision.CandidateCount)
	} else if l.Metrics != nil && len(decision.DrainCandidates) > 0 {
		l.Metrics.AddScaleDownCandidates(len(decision.DrainCandidates))
	}

	if err := ApplyScaleDown(ctx, l.Pools, decision, l.now()); err != nil {
		l.recordDrainResult("error")
		if l.Log != nil {
			l.Log.Warn("node scale-down apply failed",
				"span", "autoscaler.node.scale_down",
				"pool", decision.PoolName,
				"action", decision.Action,
				"error", err.Error(),
			)
		}
		return
	}

	l.recordDrainResult(drainResultLabel(decision))
	l.publishScaleDown(ctx, decision)
	if l.Log != nil {
		l.Log.Info("node scale-down evaluated",
			"span", "autoscaler.node.scale_down",
			"action", decision.Action,
			"pool", decision.PoolName,
			"node", decision.NodeID,
			"desired_nodes", decision.DesiredNodes,
			"ready_nodes", decision.ReadyNodes,
			"candidates", decision.CandidateCount,
			"operation_id", decision.OperationID,
			"reason", decision.Reason,
		)
	}
}

func drainResultLabel(d ScaleDownDecision) string {
	switch d.Action {
	case "scale_down":
		return "started"
	case "completed":
		return "completed"
	case "idempotent", "in_progress":
		return "in_progress"
	case "canceled":
		return "canceled"
	case "blocked":
		return "blocked"
	case "cooldown":
		return "cooldown"
	case "delete_blocked":
		return "delete_blocked"
	case "none":
		return "none"
	default:
		return d.Action
	}
}

func (l *Loop) recordScaleUpResult(pool, result string) {
	if l.Metrics == nil {
		return
	}
	if pool == "" {
		pool = "_"
	}
	l.Metrics.IncNodeScaleUpRequest(pool, result)
}

func (l *Loop) recordDrainResult(result string) {
	if l.Metrics == nil {
		return
	}
	l.Metrics.IncNodeDrains(result)
}

func (l *Loop) publishScaleUp(ctx context.Context, d ScaleUpDecision) {
	if l.Events == nil {
		return
	}
	_ = l.Events.Publish(ctx, audit.NewEvent(
		"resource.nodepool.decided",
		"",
		"",
		d.PoolName,
		map[string]any{
			"action":       d.Action,
			"desiredNodes": d.DesiredNodes,
			"readyNodes":   d.ReadyNodes,
			"pending":      d.PendingCount,
			"operationId":  d.OperationID,
			"reason":       d.Reason,
			"condition":    d.Condition,
			"direction":    "scale_up",
		},
		l.now(),
	))
}

func (l *Loop) publishScaleDown(ctx context.Context, d ScaleDownDecision) {
	if l.Events == nil {
		return
	}
	_ = l.Events.Publish(ctx, audit.NewEvent(
		"resource.nodepool.decided",
		"",
		"",
		d.PoolName,
		map[string]any{
			"action":       d.Action,
			"desiredNodes": d.DesiredNodes,
			"readyNodes":   d.ReadyNodes,
			"nodeId":       d.NodeID,
			"candidates":   d.CandidateCount,
			"operationId":  d.OperationID,
			"reason":       d.Reason,
			"condition":    d.Condition,
			"direction":    "scale_down",
		},
		l.now(),
	))
	switch d.Action {
	case "scale_down":
		_ = l.Events.Publish(ctx, audit.NewEvent(
			"node.drain.started",
			"",
			"",
			d.PoolName,
			map[string]any{
				"nodeId":      d.NodeID,
				"operationId": d.OperationID,
				"pool":        d.PoolName,
				"reason":      d.Reason,
			},
			l.now(),
		))
	case "completed":
		_ = l.Events.Publish(ctx, audit.NewEvent(
			"node.drain.completed",
			"",
			"",
			d.PoolName,
			map[string]any{
				"nodeId":      d.NodeID,
				"operationId": d.OperationID,
				"pool":        d.PoolName,
				"reason":      d.Reason,
			},
			l.now(),
		))
	}
}
