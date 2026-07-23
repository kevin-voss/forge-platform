package node

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/actuate"
)

// ScaleDownDecision is the pure outcome of one scale-down evaluation.
type ScaleDownDecision struct {
	Action          string // none|scale_down|blocked|canceled|cooldown|idempotent|in_progress|delete_blocked
	PoolName        string
	NodeID          string
	DesiredNodes    int
	ReadyNodes      int
	Draining        int
	OperationID     string
	CandidateCount  int
	Reason          string
	Condition       string // ScaleDownBlocked|StatefulPrimaryProtected|DisruptionBudgetBlocked|DeleteBlocked|ScaleDownCooldown|ScaleDownCanceled|""
	DrainCandidates []string
	Utilization     float64
}

// ScaleDownInput feeds EvaluateScaleDown.
type ScaleDownInput struct {
	Pending                 []PendingWorkload
	Fleet                   []FleetNode
	Pools                   []actuate.NodePoolView
	Cooldown                time.Duration
	UnderutilThreshold      float64
	UnderutilWindow         time.Duration
	MaxDeletesPerWindow     int
	Now                     time.Time
	ActiveDeployment        bool
	ActiveDeploymentReason  string
	DisruptionBudgetBlocked bool
	DisruptionBudgetReason  string
	StatefulBlocked         map[string]bool // nodeID → blocked
	StatefulReason          string
	RetryUncordonOnBlock    bool
}

type poolCandidate struct {
	pool    actuate.NodePoolView
	ready   int
	desired int
	floor   int
}

// EvaluateScaleDown selects an underutilized node and computes the next desiredNodes.
func EvaluateScaleDown(in ScaleDownInput) ScaleDownDecision {
	threshold := in.UnderutilThreshold
	if threshold <= 0 {
		threshold = 0.25
	}
	window := in.UnderutilWindow
	if window <= 0 {
		window = 5 * time.Minute
	}
	cooldown := in.Cooldown
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}
	maxDeletes := in.MaxDeletesPerWindow
	if maxDeletes <= 0 {
		maxDeletes = 1
	}

	if len(in.Pending) > 0 {
		return ScaleDownDecision{Action: "none", Reason: "pending_demand"}
	}
	if in.ActiveDeployment {
		reason := in.ActiveDeploymentReason
		if reason == "" {
			reason = "ActiveDeployment"
		}
		return ScaleDownDecision{
			Action:    "blocked",
			Reason:    reason,
			Condition: "ScaleDownBlocked",
		}
	}
	if in.DisruptionBudgetBlocked {
		reason := in.DisruptionBudgetReason
		if reason == "" {
			reason = "DisruptionBudgetBlocked"
		}
		return ScaleDownDecision{
			Action:    "blocked",
			Reason:    reason,
			Condition: "DisruptionBudgetBlocked",
		}
	}

	excess := poolsWithExcess(in.Pools)
	if len(excess) == 0 {
		return ScaleDownDecision{Action: "none", Reason: "at_min_nodes"}
	}

	// Resume / recover in-progress drains first (restart safety).
	for _, pc := range excess {
		if d := evaluateInProgress(pc, in); d != nil {
			return *d
		}
	}

	candidates := scoreCandidates(in.Fleet, threshold)
	if len(candidates) == 0 {
		return ScaleDownDecision{Action: "none", Reason: "no_underutilized_nodes"}
	}

	for _, pc := range excess {
		eligible, statefulOnly := filterCandidatesForPool(candidates, pc, in)
		if statefulOnly {
			return ScaleDownDecision{
				Action:         "blocked",
				PoolName:       pc.pool.Name,
				ReadyNodes:     pc.ready,
				DesiredNodes:   pc.desired,
				CandidateCount: len(candidates),
				Reason:         "StatefulPrimaryProtected",
				Condition:      "StatefulPrimaryProtected",
			}
		}
		if len(eligible) == 0 {
			continue
		}

		victim, windowReady := pickVictimWithWindow(pc.pool, eligible, in.Now, window)
		if victim == nil {
			return ScaleDownDecision{
				Action:          "none",
				PoolName:        pc.pool.Name,
				ReadyNodes:      pc.ready,
				DesiredNodes:    pc.desired,
				CandidateCount:  len(eligible),
				Reason:          "underutilization_window",
				DrainCandidates: candidateIDs(eligible),
			}
		}
		if !windowReady {
			return ScaleDownDecision{
				Action:          "none",
				PoolName:        pc.pool.Name,
				NodeID:          victim.ID,
				ReadyNodes:      pc.ready,
				DesiredNodes:    pc.desired,
				CandidateCount:  len(eligible),
				Reason:          "underutilization_window",
				Utilization:     victim.Utilization(),
				DrainCandidates: []string{victim.ID},
			}
		}

		if blocked, reason := statefulBlocked(in, *victim); blocked {
			return ScaleDownDecision{
				Action:         "blocked",
				PoolName:       pc.pool.Name,
				NodeID:         victim.ID,
				ReadyNodes:     pc.ready,
				DesiredNodes:   pc.desired,
				CandidateCount: len(eligible),
				Reason:         reason,
				Condition:      "StatefulPrimaryProtected",
				Utilization:    victim.Utilization(),
			}
		}

		// Only live workloads need replacement capacity. Stale reserved slots on
		// an empty victim must not block drain (Control heartbeats never shrink).
		needed := len(victim.RunningReplicas)
		if needed > 0 && FreeSlotsElsewhere(in.Fleet, victim.ID) < needed {
			return ScaleDownDecision{
				Action:         "canceled",
				PoolName:       pc.pool.Name,
				NodeID:         victim.ID,
				ReadyNodes:     pc.ready,
				DesiredNodes:   pc.desired,
				CandidateCount: len(eligible),
				Reason:         "replacement_placement_unavailable",
				Condition:      "ScaleDownCanceled",
				Utilization:    victim.Utilization(),
			}
		}

		deletes := actuate.StatusInt(pc.pool, "scaleDownDeletesInWindow")
		lastAt := parseTime(actuate.StatusString(pc.pool, "lastScaleDownAt"))
		if !lastAt.IsZero() && in.Now.Sub(lastAt) < cooldown {
			if deletes >= maxDeletes {
				return ScaleDownDecision{
					Action:       "cooldown",
					PoolName:     pc.pool.Name,
					ReadyNodes:   pc.ready,
					DesiredNodes: pc.desired,
					OperationID:  actuate.StatusString(pc.pool, "lastScaleDownOperationId"),
					Reason:       "scale_down_cooldown",
					Condition:    "ScaleDownCooldown",
				}
			}
		}

		opID := ScaleDownWindowID(pc.pool.Name, victim.ID)
		prevOp := actuate.StatusString(pc.pool, "lastScaleDownOperationId")
		prevNode := actuate.StatusString(pc.pool, "drainCandidateNodeId")
		if prevOp == opID && prevNode == victim.ID && pc.desired < pc.ready {
			return ScaleDownDecision{
				Action:          "idempotent",
				PoolName:        pc.pool.Name,
				NodeID:          victim.ID,
				DesiredNodes:    pc.desired,
				ReadyNodes:      pc.ready,
				Draining:        pc.ready - pc.desired,
				OperationID:     opID,
				CandidateCount:  len(eligible),
				Reason:          "scale_down_in_flight",
				Utilization:     victim.Utilization(),
				DrainCandidates: []string{victim.ID},
			}
		}

		target := pc.desired - 1
		if target < pc.floor {
			target = pc.floor
		}
		if target >= pc.desired {
			return ScaleDownDecision{Action: "none", PoolName: pc.pool.Name, Reason: "already_at_floor"}
		}

		return ScaleDownDecision{
			Action:          "scale_down",
			PoolName:        pc.pool.Name,
			NodeID:          victim.ID,
			DesiredNodes:    target,
			ReadyNodes:      pc.ready,
			Draining:        1,
			OperationID:     opID,
			CandidateCount:  len(eligible),
			Reason:          fmt.Sprintf("underutilized util=%.2f threshold=%.2f", victim.Utilization(), threshold),
			Utilization:     victim.Utilization(),
			DrainCandidates: []string{victim.ID},
		}
	}

	return ScaleDownDecision{Action: "none", Reason: "no_eligible_candidate", CandidateCount: len(candidates)}
}

func poolsWithExcess(pools []actuate.NodePoolView) []poolCandidate {
	var excess []poolCandidate
	for _, pool := range pools {
		ready := actuate.StatusInt(pool, "readyNodes")
		if ready == 0 {
			ready = actuate.StatusInt(pool, "currentNodes")
		}
		desired := actuate.StatusInt(pool, "desiredNodes")
		spec := actuate.SpecReplicas(pool)
		drainInFlight := actuate.StatusString(pool, "drainCandidateNodeId") != "" ||
			strings.EqualFold(actuate.StatusString(pool, "scaleDownPhase"), "draining") ||
			strings.EqualFold(actuate.StatusString(pool, "scaleDownPhase"), "deleting") ||
			strings.EqualFold(actuate.StatusString(pool, "scaleDownPhase"), "delete_blocked")
		if drainInFlight {
			// Keep autoscaler-owned desiredNodes while a drain converges.
			if desired == 0 {
				desired = spec
			}
			// Once SetReplicas lowered the floor, prefer that target so status
			// does not stay pinned at the pre-drain ready count.
			if spec > 0 && (desired == 0 || spec < desired) {
				desired = spec
			}
		} else {
			if desired < spec {
				desired = spec
			}
			if desired < ready {
				desired = ready
			}
		}
		floor := actuate.MinNodes(pool)
		if floor < 1 {
			floor = 1
		}
		// Block only while create is still outstanding (ready behind desired).
		// After Ready catches up, a leftover creatingNodes counter must not
		// permanently suppress scale-down (common after reservation scale-up).
		if creating := actuate.StatusInt(pool, "creatingNodes"); creating > 0 && ready < desired {
			continue
		}
		// In-flight drains stay eligible even when desired already at floor.
		if !drainInFlight && (ready <= floor || desired <= floor) {
			continue
		}
		if drainInFlight && ready <= floor && desired >= ready {
			// Fully converged at floor with no remaining excess.
			phase := strings.ToLower(actuate.StatusString(pool, "scaleDownPhase"))
			if phase != "draining" && phase != "deleting" && phase != "delete_blocked" {
				continue
			}
		}
		excess = append(excess, poolCandidate{pool: pool, ready: ready, desired: desired, floor: floor})
	}
	sort.SliceStable(excess, func(i, j int) bool {
		// Prefer pools with more excess nodes.
		ei, ej := excess[i].ready-excess[i].floor, excess[j].ready-excess[j].floor
		if ei != ej {
			return ei > ej
		}
		return excess[i].pool.Name < excess[j].pool.Name
	})
	return excess
}

func evaluateInProgress(pc poolCandidate, in ScaleDownInput) *ScaleDownDecision {
	cand := actuate.StatusString(pc.pool, "drainCandidateNodeId")
	opID := actuate.StatusString(pc.pool, "lastScaleDownOperationId")
	phase := strings.ToLower(actuate.StatusString(pc.pool, "scaleDownPhase"))
	if cand == "" || opID == "" {
		return nil
	}

	var victim *FleetNode
	for i := range in.Fleet {
		if in.Fleet[i].ID == cand {
			victim = &in.Fleet[i]
			break
		}
	}

	// Drain completed: candidate gone and ready converged to desired.
	if victim == nil && pc.ready <= pc.desired && (phase == "draining" || phase == "deleting" || phase == "") {
		return &ScaleDownDecision{
			Action:          "completed",
			PoolName:        pc.pool.Name,
			NodeID:          cand,
			DesiredNodes:    pc.desired,
			ReadyNodes:      pc.ready,
			OperationID:     opID,
			Reason:          "drain_completed",
			DrainCandidates: nil,
		}
	}

	if pc.desired >= pc.ready && phase != "deleting" && phase != "draining" && phase != "delete_blocked" {
		return nil
	}

	// DeleteBlocked: desired lowered, node still present, delete phase stuck.
	if phase == "delete_blocked" || actuate.StatusString(pc.pool, "scaleDownCondition") == "DeleteBlocked" {
		return &ScaleDownDecision{
			Action:          "delete_blocked",
			PoolName:        pc.pool.Name,
			NodeID:          cand,
			DesiredNodes:    pc.desired,
			ReadyNodes:      pc.ready,
			OperationID:     opID,
			Reason:          "provider_delete_blocked",
			Condition:       "DeleteBlocked",
			DrainCandidates: []string{cand},
		}
	}

	if victim != nil {
		if blocked, reason := statefulBlocked(in, *victim); blocked {
			action := "blocked"
			desired := pc.desired
			if in.RetryUncordonOnBlock {
				// Uncordon: restore desired to ready so Infrastructure stops drain.
				desired = pc.ready
				action = "canceled"
			}
			return &ScaleDownDecision{
				Action:          action,
				PoolName:        pc.pool.Name,
				NodeID:          cand,
				DesiredNodes:    desired,
				ReadyNodes:      pc.ready,
				OperationID:     opID,
				Reason:          reason,
				Condition:       "StatefulPrimaryProtected",
				DrainCandidates: nil,
			}
		}
		needed := len(victim.RunningReplicas)
		if needed > 0 && FreeSlotsElsewhere(in.Fleet, victim.ID) < needed {
			return &ScaleDownDecision{
				Action:          "canceled",
				PoolName:        pc.pool.Name,
				NodeID:          cand,
				DesiredNodes:    pc.ready, // uncordon / cancel
				ReadyNodes:      pc.ready,
				OperationID:     opID,
				Reason:          "replacement_placement_unavailable",
				Condition:       "ScaleDownCanceled",
				DrainCandidates: nil,
			}
		}
	}

	// Still converging — keep desired, same operation id (idempotent delete).
	return &ScaleDownDecision{
		Action:          "in_progress",
		PoolName:        pc.pool.Name,
		NodeID:          cand,
		DesiredNodes:    pc.desired,
		ReadyNodes:      pc.ready,
		Draining:        max(0, pc.ready-pc.desired),
		OperationID:     opID,
		Reason:          "drain_in_progress",
		DrainCandidates: []string{cand},
	}
}

func statefulBlocked(in ScaleDownInput, victim FleetNode) (bool, string) {
	if in.StatefulBlocked != nil && in.StatefulBlocked[victim.ID] {
		reason := in.StatefulReason
		if reason == "" {
			reason = "StatefulPrimaryProtected"
		}
		return true, reason
	}
	if looksLikeStatefulPrimary(victim) {
		return true, "StatefulPrimaryProtected"
	}
	return false, ""
}

func scoreCandidates(fleet []FleetNode, threshold float64) []FleetNode {
	var out []FleetNode
	for _, n := range fleet {
		if !n.IsReady() {
			continue
		}
		if n.Utilization() <= threshold {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ui, uj := out[i].Utilization(), out[j].Utilization()
		if ui != uj {
			return ui < uj
		}
		// Prefer empty, then newest (most recent name/id) to match infra victim bias.
		if out[i].IsEmpty() != out[j].IsEmpty() {
			return out[i].IsEmpty()
		}
		if !out[i].RegisteredAt.Equal(out[j].RegisteredAt) {
			return out[i].RegisteredAt.After(out[j].RegisteredAt)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func filterCandidatesForPool(candidates []FleetNode, pc poolCandidate, in ScaleDownInput) (eligible []FleetNode, statefulOnly bool) {
	var out []FleetNode
	statefulHits := 0
	for _, n := range candidates {
		if blocked, _ := statefulBlocked(in, n); blocked {
			statefulHits++
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 && statefulHits > 0 {
		return nil, true
	}
	// When fleet has no pool labels, all underutilized ready nodes are eligible
	// for any excess pool (single-pool demos). Multi-pool installs should label nodes.
	if len(out) == 0 {
		return out, false
	}
	poolLabel := ""
	if labels := actuate.PoolLabels(pc.pool); labels != nil {
		poolLabel = labels["nodepool"]
		if poolLabel == "" {
			poolLabel = labels["forge.dev/nodepool"]
		}
	}
	if poolLabel == "" {
		return out, false
	}
	filtered := make([]FleetNode, 0, len(out))
	for _, n := range out {
		if n.Labels["nodepool"] == poolLabel || n.Labels["forge.dev/nodepool"] == poolLabel || n.Labels["nodePool"] == pc.pool.Name {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		// Fall back to unlabeled candidates when pool labeling is absent on nodes.
		return out, false
	}
	return filtered, false
}

func pickVictimWithWindow(pool actuate.NodePoolView, eligible []FleetNode, now time.Time, window time.Duration) (*FleetNode, bool) {
	if len(eligible) == 0 {
		return nil, false
	}
	// Prefer continuing the same candidate when already tracked.
	tracked := actuate.StatusString(pool, "underutilizedNodeId")
	var victim *FleetNode
	for i := range eligible {
		if eligible[i].ID == tracked {
			victim = &eligible[i]
			break
		}
	}
	if victim == nil {
		victim = &eligible[0]
	}
	since := parseTime(actuate.StatusString(pool, "underutilizedSince"))
	prev := actuate.StatusString(pool, "underutilizedNodeId")
	if prev != victim.ID || since.IsZero() {
		return victim, false
	}
	return victim, !now.Before(since.Add(window))
}

func candidateIDs(nodes []FleetNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ApplyScaleDown actuates Infrastructure via spec.replicas and writes NodePool status.
func ApplyScaleDown(
	ctx context.Context,
	pools actuate.NodePoolActuator,
	decision ScaleDownDecision,
	now time.Time,
) error {
	if decision.PoolName == "" {
		// Cluster-level blocked (no pool) — nothing to write.
		return nil
	}
	view, err := pools.Get(ctx, decision.PoolName)
	if err != nil {
		return err
	}

	switch decision.Action {
	case "scale_down", "canceled":
		if decision.DesiredNodes > 0 || decision.Action == "canceled" {
			target := decision.DesiredNodes
			if target < 0 {
				target = 0
			}
			if _, err := pools.SetReplicas(ctx, decision.PoolName, target, decision.OperationID); err != nil {
				return err
			}
			view, err = pools.Get(ctx, decision.PoolName)
			if err != nil {
				return err
			}
		}
	case "idempotent", "in_progress", "cooldown", "blocked", "delete_blocked", "completed", "none":
		// status-only
	}

	status := mergeScaleDownStatus(view.Status, decision, now)
	_, err = pools.PutStatus(ctx, decision.PoolName, view.ResourceVersion, status)
	return err
}

func mergeScaleDownStatus(existing map[string]any, d ScaleDownDecision, now time.Time) map[string]any {
	status := map[string]any{}
	for k, v := range existing {
		status[k] = v
	}

	if d.DesiredNodes > 0 || d.Action == "canceled" || d.Action == "scale_down" {
		status["desiredNodes"] = d.DesiredNodes
	}
	status["currentNodes"] = d.ReadyNodes
	status["readyNodes"] = d.ReadyNodes
	if d.Draining > 0 {
		status["drainingNodes"] = d.Draining
	}

	switch d.Action {
	case "scale_down":
		status["lastScaleDownOperationId"] = d.OperationID
		status["lastScaleDownAt"] = now.UTC().Format(time.RFC3339)
		status["drainCandidateNodeId"] = d.NodeID
		status["scaleDownPhase"] = "draining"
		status["scaleDownDeletesInWindow"] = actuateStatusInt(existing, "scaleDownDeletesInWindow") + 1
		status["underutilizedNodeId"] = d.NodeID
		if asString(existing["underutilizedNodeId"]) != d.NodeID || asString(existing["underutilizedSince"]) == "" {
			status["underutilizedSince"] = now.UTC().Format(time.RFC3339)
		}
	case "completed":
		status["scaleDownPhase"] = "completed"
		delete(status, "drainCandidateNodeId")
		status["drainCandidates"] = []any{}
		status["drainingNodes"] = 0
		if d.OperationID != "" {
			status["lastScaleDownOperationId"] = d.OperationID
		}
	case "idempotent", "in_progress", "delete_blocked":
		if d.OperationID != "" {
			status["lastScaleDownOperationId"] = d.OperationID
		}
		if d.NodeID != "" {
			status["drainCandidateNodeId"] = d.NodeID
		}
		if d.Action == "delete_blocked" {
			status["scaleDownPhase"] = "delete_blocked"
		} else if d.Action == "in_progress" {
			status["scaleDownPhase"] = "draining"
		}
	case "canceled":
		status["scaleDownPhase"] = "canceled"
		delete(status, "drainCandidateNodeId")
		status["drainCandidates"] = []any{}
	case "blocked":
		status["scaleDownPhase"] = "blocked"
		delete(status, "drainCandidateNodeId")
	case "none":
		if d.Reason == "underutilization_window" && d.NodeID != "" {
			prev := asString(existing["underutilizedNodeId"])
			if prev != d.NodeID {
				status["underutilizedNodeId"] = d.NodeID
				status["underutilizedSince"] = now.UTC().Format(time.RFC3339)
			} else if asString(existing["underutilizedSince"]) == "" {
				status["underutilizedSince"] = now.UTC().Format(time.RFC3339)
			} else {
				status["underutilizedNodeId"] = prev
				status["underutilizedSince"] = existing["underutilizedSince"]
			}
			if len(d.DrainCandidates) > 0 {
				status["drainCandidates"] = toAnySlice(d.DrainCandidates)
			}
		}
	}

	if len(d.DrainCandidates) > 0 && d.Action != "canceled" && d.Action != "blocked" {
		status["drainCandidates"] = toAnySlice(d.DrainCandidates)
	}

	status["scaleDownRecommendation"] = map[string]any{
		"action":      d.Action,
		"reason":      d.Reason,
		"operationId": d.OperationID,
		"nodeId":      d.NodeID,
		"utilization": d.Utilization,
		"at":          now.UTC().Format(time.RFC3339),
	}

	conds := extractConditions(existing)
	conds = upsertCondition(conds, "ScaleDownRecommended", boolStatus(d.Action == "scale_down" || d.Action == "idempotent" || d.Action == "in_progress"), d.Action, d.Reason, now)
	conds = upsertCondition(conds, "ScaleDownBlocked", boolStatus(d.Condition == "ScaleDownBlocked" || d.Condition == "DisruptionBudgetBlocked"), d.Condition, d.Reason, now)
	conds = upsertCondition(conds, "StatefulPrimaryProtected", boolStatus(d.Condition == "StatefulPrimaryProtected"), d.Condition, d.Reason, now)
	conds = upsertCondition(conds, "DisruptionBudgetBlocked", boolStatus(d.Condition == "DisruptionBudgetBlocked"), d.Condition, d.Reason, now)
	conds = upsertCondition(conds, "ScaleDownCanceled", boolStatus(d.Condition == "ScaleDownCanceled" || d.Action == "canceled"), d.Action, d.Reason, now)
	conds = upsertCondition(conds, "ScaleDownCooldown", boolStatus(d.Condition == "ScaleDownCooldown"), d.Condition, d.Reason, now)
	conds = upsertCondition(conds, "DeleteBlocked", boolStatus(d.Condition == "DeleteBlocked" || d.Action == "delete_blocked"), d.Condition, d.Reason, now)
	status["conditions"] = conds
	return status
}

func actuateStatusInt(status map[string]any, key string) int {
	if n, ok := asInt(status[key]); ok {
		return n
	}
	return 0
}

func toAnySlice(ids []string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}
