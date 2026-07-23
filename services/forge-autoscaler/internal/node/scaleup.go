package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
)

// ScaleUpDecision is the pure outcome of one scale-up evaluation.
type ScaleUpDecision struct {
	Action       string // none|scale_up|cooldown|idempotent|no_eligible|capacity_blocked|reservation_hold
	PoolName     string
	DesiredNodes int
	ReadyNodes   int
	Creating     int
	Failed       int
	OperationID  string
	PendingCount int
	Reason       string
	Condition    string // NoEligibleNodePool|ProviderCapacityBlocked|ScaleUpCooldown|""
}

// ScaleUpInput feeds EvaluateScaleUp.
type ScaleUpInput struct {
	Pending              []PendingWorkload
	Reservation          ClusterReservation
	Pools                []actuate.NodePoolView
	ReservationThreshold float64
	Cooldown             time.Duration
	Now                  time.Time
	DefaultMaxNodes      int
}

// EvaluateScaleUp selects a pool and computes the next desiredNodes without side effects.
func EvaluateScaleUp(in ScaleUpInput) ScaleUpDecision {
	threshold := in.ReservationThreshold
	if threshold <= 0 {
		threshold = 0.85
	}
	pending := in.Pending
	reservationBreach := in.Reservation.Ratio() >= threshold && in.Reservation.CapacitySlots > 0

	if len(pending) == 0 && !reservationBreach {
		return ScaleUpDecision{Action: "none", Reason: "no_pending_demand"}
	}

	demand := AggregateDemand(pending)
	if len(pending) == 0 && reservationBreach {
		// Proactive: request one node on the highest-priority unrestricted pool.
		demand.Slots = SlotsPerNode(pickAny(in.Pools))
	}

	sel := SelectNodePool(in.Pools, demand)
	if sel.Pool == nil {
		return ScaleUpDecision{
			Action:       "no_eligible",
			PendingCount: len(pending),
			Reason:       "NoEligibleNodePool",
			Condition:    "NoEligibleNodePool",
		}
	}
	pool := *sel.Pool
	if actuate.HasProviderCapacityBlocked(pool) {
		return ScaleUpDecision{
			Action:       "capacity_blocked",
			PoolName:     pool.Name,
			PendingCount: len(pending),
			Reason:       "ProviderCapacityBlocked",
			Condition:    "ProviderCapacityBlocked",
		}
	}

	ready := actuate.StatusInt(pool, "readyNodes")
	if ready == 0 {
		ready = actuate.StatusInt(pool, "currentNodes")
	}
	currentDesired := actuate.StatusInt(pool, "desiredNodes")
	specReplicas := actuate.SpecReplicas(pool)
	if currentDesired < specReplicas {
		currentDesired = specReplicas
	}
	if currentDesired < ready {
		currentDesired = ready
	}
	minNodes := actuate.MinNodes(pool)
	if currentDesired < minNodes {
		currentDesired = minNodes
	}

	slotsPerNode := SlotsPerNode(pool)
	needed := NodesNeeded(demand.Slots, slotsPerNode)
	if needed == 0 && reservationBreach {
		needed = 1
	}
	if needed == 0 {
		return ScaleUpDecision{Action: "none", PoolName: pool.Name, Reason: "zero_nodes_needed", ReadyNodes: ready}
	}

	maxNodes := actuate.MaxNodes(pool, in.DefaultMaxNodes)
	target := currentDesired + needed
	// If already creating toward a higher desired, don't stack more for the same window.
	creating := currentDesired - ready
	if creating < 0 {
		creating = 0
	}
	if creating > 0 && currentDesired >= ready+needed {
		target = currentDesired
	}
	if target > maxNodes {
		target = maxNodes
	}
	if target <= currentDesired && creating > 0 {
		opID := actuate.StatusString(pool, "lastScaleUpOperationId")
		if opID == "" {
			opID = DemandWindowID(pool.Name, pending)
		}
		return ScaleUpDecision{
			Action:       "idempotent",
			PoolName:     pool.Name,
			DesiredNodes: currentDesired,
			ReadyNodes:   ready,
			Creating:     creating,
			OperationID:  opID,
			PendingCount: len(pending),
			Reason:       "scale_up_in_flight",
		}
	}
	if target <= currentDesired {
		return ScaleUpDecision{
			Action:       "none",
			PoolName:     pool.Name,
			DesiredNodes: currentDesired,
			ReadyNodes:   ready,
			PendingCount: len(pending),
			Reason:       "already_at_desired",
		}
	}

	opID := DemandWindowID(pool.Name, pending)
	prevOp := actuate.StatusString(pool, "lastScaleUpOperationId")
	if prevOp == opID && actuate.StatusInt(pool, "desiredNodes") >= target {
		return ScaleUpDecision{
			Action:       "idempotent",
			PoolName:     pool.Name,
			DesiredNodes: target,
			ReadyNodes:   ready,
			Creating:     target - ready,
			OperationID:  opID,
			PendingCount: len(pending),
			Reason:       "duplicate_demand_window",
		}
	}

	cooldown := in.Cooldown
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	if lastAt := parseTime(actuate.StatusString(pool, "lastScaleUpAt")); !lastAt.IsZero() {
		if in.Now.Sub(lastAt) < cooldown && prevOp != "" && prevOp != opID {
			return ScaleUpDecision{
				Action:       "cooldown",
				PoolName:     pool.Name,
				DesiredNodes: currentDesired,
				ReadyNodes:   ready,
				Creating:     creating,
				OperationID:  prevOp,
				PendingCount: len(pending),
				Reason:       "scale_up_cooldown",
				Condition:    "ScaleUpCooldown",
			}
		}
	}

	return ScaleUpDecision{
		Action:       "scale_up",
		PoolName:     pool.Name,
		DesiredNodes: target,
		ReadyNodes:   ready,
		Creating:     target - ready,
		Failed:       actuate.StatusInt(pool, "failedNodes"),
		OperationID:  opID,
		PendingCount: len(pending),
		Reason:       fmt.Sprintf("pending=%d reservation=%.2f", len(pending), in.Reservation.Ratio()),
	}
}

func pickAny(pools []actuate.NodePoolView) actuate.NodePoolView {
	if len(pools) == 0 {
		return actuate.NodePoolView{}
	}
	sel := SelectNodePool(pools, DemandConstraints{Slots: 1, Labels: map[string]string{}})
	if sel.Pool != nil {
		return *sel.Pool
	}
	return pools[0]
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ApplyScaleUp actuates Infrastructure via spec.replicas and writes NodePool status.
func ApplyScaleUp(
	ctx context.Context,
	pools actuate.NodePoolActuator,
	decision ScaleUpDecision,
	now time.Time,
) error {
	if decision.PoolName == "" {
		return nil
	}
	view, err := pools.Get(ctx, decision.PoolName)
	if err != nil {
		return err
	}

	switch decision.Action {
	case "scale_up":
		if _, err := pools.SetReplicas(ctx, decision.PoolName, decision.DesiredNodes, decision.OperationID); err != nil {
			return err
		}
		// Refresh RV after patch.
		view, err = pools.Get(ctx, decision.PoolName)
		if err != nil {
			return err
		}
	case "idempotent", "cooldown", "capacity_blocked", "none":
		// status-only update
	case "no_eligible":
		return nil
	}

	status := mergeScaleUpStatus(view.Status, decision, now)
	_, err = pools.PutStatus(ctx, decision.PoolName, view.ResourceVersion, status)
	return err
}

func mergeScaleUpStatus(existing map[string]any, d ScaleUpDecision, now time.Time) map[string]any {
	status := map[string]any{}
	for k, v := range existing {
		status[k] = v
	}
	// Do not clobber an in-flight scale-down target with a none/idempotent
	// scale-up status write (readyNodes may still equal the old desired).
	scaleDownPhase := strings.ToLower(asString(existing["scaleDownPhase"]))
	drainInFlight := scaleDownPhase == "draining" || scaleDownPhase == "deleting" ||
		scaleDownPhase == "delete_blocked" || asString(existing["drainCandidateNodeId"]) != ""
	if d.DesiredNodes > 0 && !(drainInFlight && (d.Action == "none" || d.Action == "idempotent")) {
		status["desiredNodes"] = d.DesiredNodes
	}
	status["currentNodes"] = d.ReadyNodes
	status["readyNodes"] = d.ReadyNodes
	if d.Creating > 0 {
		status["creatingNodes"] = d.Creating
	} else {
		status["creatingNodes"] = 0
	}
	status["failedNodes"] = d.Failed
	if d.OperationID != "" {
		status["lastScaleUpOperationId"] = d.OperationID
	}
	if d.Action == "scale_up" {
		status["lastScaleUpAt"] = now.UTC().Format(time.RFC3339)
	}
	status["pendingWorkloads"] = d.PendingCount
	status["scaleUpRecommendation"] = map[string]any{
		"action":      d.Action,
		"reason":      d.Reason,
		"operationId": d.OperationID,
		"at":          now.UTC().Format(time.RFC3339),
	}

	conds := extractConditions(existing)
	conds = upsertCondition(conds, "ScaleUpRecommended", boolStatus(d.Action == "scale_up" || d.Action == "idempotent"), d.Action, d.Reason, now)
	if d.Condition == "ProviderCapacityBlocked" || d.Action == "capacity_blocked" {
		conds = upsertCondition(conds, "ProviderCapacityBlocked", "True", "ProviderCapacityBlocked", d.Reason, now)
	} else {
		conds = upsertCondition(conds, "ProviderCapacityBlocked", "False", "CapacityAvailable", "", now)
	}
	if d.Condition == "ScaleUpCooldown" {
		conds = upsertCondition(conds, "ScaleUpCooldown", "True", "ScaleUpCooldown", d.Reason, now)
	} else {
		conds = upsertCondition(conds, "ScaleUpCooldown", "False", "CooldownElapsed", "", now)
	}
	if d.Condition == "NoEligibleNodePool" {
		conds = upsertCondition(conds, "NoEligibleNodePool", "True", "NoEligibleNodePool", d.Reason, now)
	}
	status["conditions"] = conds
	return status
}

func boolStatus(v bool) string {
	if v {
		return "True"
	}
	return "False"
}

func extractConditions(status map[string]any) []map[string]any {
	raw, ok := status["conditions"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			cp := map[string]any{}
			for k, v := range m {
				cp[k] = v
			}
			out = append(out, cp)
		}
	}
	return out
}

func upsertCondition(conds []map[string]any, ctype, cstatus, reason, message string, now time.Time) []map[string]any {
	nowStr := now.UTC().Format(time.RFC3339)
	for i := range conds {
		if asString(conds[i]["type"]) == ctype {
			prev := asString(conds[i]["status"])
			conds[i]["status"] = cstatus
			conds[i]["reason"] = reason
			conds[i]["message"] = message
			if prev != cstatus {
				conds[i]["lastTransitionTime"] = nowStr
			} else if asString(conds[i]["lastTransitionTime"]) == "" {
				conds[i]["lastTransitionTime"] = nowStr
			}
			return conds
		}
	}
	return append(conds, map[string]any{
		"type":               ctype,
		"status":             cstatus,
		"reason":             reason,
		"message":            message,
		"lastTransitionTime": nowStr,
	})
}
