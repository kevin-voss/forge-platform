package node

import (
	"context"
	"log/slog"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
	"forge.local/services/forge-autoscaler/internal/audit"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

// DemandReader loads pending + reservation signals.
type DemandReader interface {
	ListPending(ctx context.Context) ([]PendingWorkload, error)
	ClusterReservation(ctx context.Context) (ClusterReservation, error)
}

// Loop periodically evaluates node scale-up.
type Loop struct {
	Signals              DemandReader
	Pools                actuate.NodePoolActuator
	Metrics              *telemetry.Registry
	Events               audit.Publisher
	Interval             time.Duration
	Cooldown             time.Duration
	ReservationThreshold float64
	DefaultMaxNodes      int
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

// Tick runs one scale-up evaluation.
func (l *Loop) Tick(ctx context.Context) {
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
		l.recordResult("", "no_eligible")
		l.publish(ctx, decision)
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
		l.recordResult(decision.PoolName, "error")
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
	l.recordResult(decision.PoolName, result)
	l.publish(ctx, decision)
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

func (l *Loop) recordResult(pool, result string) {
	if l.Metrics == nil {
		return
	}
	if pool == "" {
		pool = "_"
	}
	l.Metrics.IncNodeScaleUpRequest(pool, result)
}

func (l *Loop) publish(ctx context.Context, d ScaleUpDecision) {
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
		},
		l.now(),
	))
}
