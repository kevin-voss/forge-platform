package node

import (
	"sort"
	"strings"

	"forge.local/services/forge-autoscaler/internal/actuate"
)

// DemandConstraints aggregates pending workload requirements for pool selection.
type DemandConstraints struct {
	Region       string
	Architecture string
	GPU          int
	Labels       map[string]string
	Slots        int
}

// AggregateDemand merges constraints from pending workloads (union / max).
func AggregateDemand(pending []PendingWorkload) DemandConstraints {
	d := DemandConstraints{Labels: map[string]string{}, Slots: PendingSlots(pending)}
	for _, p := range pending {
		if d.Region == "" && p.Region != "" {
			d.Region = p.Region
		}
		if d.Architecture == "" && p.Architecture != "" {
			d.Architecture = p.Architecture
		}
		if p.GPU > d.GPU {
			d.GPU = p.GPU
		}
		for k, v := range p.Labels {
			if v != "" {
				d.Labels[k] = v
			}
		}
	}
	return d
}

// SelectResult is the outcome of NodePool eligibility scoring.
type SelectResult struct {
	Pool     *actuate.NodePoolView
	Eligible []actuate.NodePoolView
	Reason   string
}

// SelectNodePool picks the lowest-priority eligible pool for the demand.
// Returns Reason=NoEligibleNodePool when nothing matches.
func SelectNodePool(pools []actuate.NodePoolView, demand DemandConstraints) SelectResult {
	var eligible []actuate.NodePoolView
	for i := range pools {
		pool := pools[i]
		if ok, _ := Eligible(pool, demand); ok {
			eligible = append(eligible, pool)
		}
	}
	if len(eligible) == 0 {
		return SelectResult{Reason: "NoEligibleNodePool"}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		pi, pj := actuate.Priority(eligible[i]), actuate.Priority(eligible[j])
		if pi != pj {
			return pi < pj
		}
		return eligible[i].Name < eligible[j].Name
	})
	best := eligible[0]
	return SelectResult{Pool: &best, Eligible: eligible, Reason: "selected"}
}

// Eligible reports whether a pool can satisfy demand and is not capacity-blocked.
func Eligible(pool actuate.NodePoolView, demand DemandConstraints) (bool, string) {
	if actuate.HasProviderCapacityBlocked(pool) {
		return false, "ProviderCapacityBlocked"
	}
	maxNodes := actuate.MaxNodes(pool, 100)
	ready := actuate.StatusInt(pool, "readyNodes")
	if ready == 0 {
		ready = actuate.StatusInt(pool, "currentNodes")
	}
	desired := actuate.StatusInt(pool, "desiredNodes")
	if desired == 0 {
		desired = actuate.SpecReplicas(pool)
	}
	if desired >= maxNodes && ready >= maxNodes {
		return false, "MaxNodesReached"
	}

	if demand.Region != "" {
		region := actuate.Region(pool)
		if region != "" && !strings.EqualFold(region, demand.Region) {
			return false, "RegionMismatch"
		}
	}
	if demand.Architecture != "" {
		arch := actuate.Architecture(pool)
		if arch != "" && !strings.EqualFold(arch, demand.Architecture) {
			return false, "ArchitectureMismatch"
		}
	}
	if demand.GPU > 0 && actuate.GPUCount(pool) < demand.GPU {
		return false, "GPUMismatch"
	}

	poolLabels := actuate.PoolLabels(pool)
	selector := actuate.MachineSelector(pool)
	// Demand labels must be satisfied by pool labels.
	for k, v := range demand.Labels {
		if pv, ok := poolLabels[k]; !ok || pv != v {
			return false, "LabelMismatch"
		}
	}
	// Pool machineSelector constrains which demand labels are accepted when present.
	for k, v := range selector {
		if dv, ok := demand.Labels[k]; ok && dv != v {
			return false, "SelectorMismatch"
		}
	}
	return true, ""
}

// SlotsPerNode estimates capacity per new node from machine type or defaults.
func SlotsPerNode(pool actuate.NodePoolView) int {
	mt := strings.ToLower(actuate.MachineType(pool))
	switch mt {
	case "docker-small":
		return 2
	case "docker-medium":
		return 4
	case "docker-large":
		return 8
	}
	if machine, ok := pool.Spec["machine"].(map[string]any); ok {
		if n, ok := asInt(machine["slots"]); ok && n > 0 {
			return n
		}
		// Rough: 1 slot per CPU core when slots absent.
		if n, ok := asInt(machine["cpu"]); ok && n > 0 {
			return n
		}
	}
	if n, ok := asInt(pool.Spec["slotsPerNode"]); ok && n > 0 {
		return n
	}
	return 2
}

// NodesNeeded computes how many additional nodes are required for pending slots.
func NodesNeeded(pendingSlots, slotsPerNode int) int {
	if pendingSlots <= 0 {
		return 0
	}
	if slotsPerNode <= 0 {
		slotsPerNode = 1
	}
	return (pendingSlots + slotsPerNode - 1) / slotsPerNode
}
