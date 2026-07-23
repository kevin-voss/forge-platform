package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// FleetNode is one Control fleet node with utilization for scale-down scoring.
type FleetNode struct {
	ID              string
	Status          string
	CapacitySlots   int
	AllocatedSlots  int
	CapacityCPU     int
	AllocatedCPU    int
	CapacityMemory  int
	AllocatedMemory int
	RunningReplicas []string
	Labels          map[string]string
	RegisteredAt    time.Time
}

// EffectiveAllocatedSlots returns the slot count used for scale-down scoring.
// When Control still holds CapacityReservation after workloads are gone (orphan
// placements / heartbeat max-only updates), prefer the live running count so
// empty nodes remain eligible victims.
func (n FleetNode) EffectiveAllocatedSlots() int {
	live := len(n.RunningReplicas)
	if live < n.AllocatedSlots {
		return live
	}
	if n.AllocatedSlots > 0 {
		return n.AllocatedSlots
	}
	return live
}

// Utilization returns the highest allocation ratio among known dimensions (0–1).
func (n FleetNode) Utilization() float64 {
	var ratios []float64
	if n.CapacitySlots > 0 {
		ratios = append(ratios, float64(n.EffectiveAllocatedSlots())/float64(n.CapacitySlots))
	}
	if n.CapacityCPU > 0 {
		ratios = append(ratios, float64(n.AllocatedCPU)/float64(n.CapacityCPU))
	}
	if n.CapacityMemory > 0 {
		ratios = append(ratios, float64(n.AllocatedMemory)/float64(n.CapacityMemory))
	}
	max := 0.0
	for _, r := range ratios {
		if r > max {
			max = r
		}
	}
	// Empty capacity with no allocations → fully idle.
	if len(ratios) == 0 {
		if n.IsEmpty() {
			return 0
		}
		return 1
	}
	return max
}

// IsReady reports whether the node can host workloads.
func (n FleetNode) IsReady() bool {
	s := strings.ToLower(strings.TrimSpace(n.Status))
	return s == "" || s == "online" || s == "ready"
}

// IsEmpty reports whether the node has no running workloads.
// Stale reserved slots alone do not count as occupancy for scale-down.
func (n FleetNode) IsEmpty() bool {
	return len(n.RunningReplicas) == 0
}

// ListFleetNodes loads Control GET /v1/nodes.
func (s *SignalSource) ListFleetNodes(ctx context.Context) ([]FleetNode, error) {
	if strings.TrimSpace(s.BaseURL) == "" {
		return nil, fmt.Errorf("control URL is not configured")
	}
	endpoint := strings.TrimRight(s.BaseURL, "/") + "/v1/nodes"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list nodes: status %d: %s", resp.StatusCode, truncate(body))
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode nodes: %w", err)
	}
	out := make([]FleetNode, 0, len(rows))
	for _, row := range rows {
		n := parseFleetNode(row)
		if n.ID == "" {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func parseFleetNode(row map[string]any) FleetNode {
	n := FleetNode{
		ID:     firstNonEmpty(asString(row["id"]), asString(row["node_id"])),
		Status: asString(row["status"]),
		Labels: map[string]string{},
	}
	if cap, ok := row["capacity"].(map[string]any); ok {
		if v, ok := asInt(cap["slots"]); ok {
			n.CapacitySlots = v
		}
		if v, ok := asInt(cap["cpu_millis"]); ok {
			n.CapacityCPU = v
		}
		if v, ok := asInt(cap["mem_mb"]); ok {
			n.CapacityMemory = v
		}
	}
	if alloc, ok := row["allocated"].(map[string]any); ok {
		if v, ok := asInt(alloc["slots"]); ok {
			n.AllocatedSlots = v
		}
		if v, ok := asInt(alloc["cpu_millis"]); ok {
			n.AllocatedCPU = v
		}
		if v, ok := asInt(alloc["mem_mb"]); ok {
			n.AllocatedMemory = v
		}
	}
	if reps, ok := row["running_replicas"].([]any); ok {
		for _, r := range reps {
			if s := asString(r); s != "" {
				n.RunningReplicas = append(n.RunningReplicas, s)
			}
		}
	}
	if labels, ok := row["labels"].(map[string]any); ok {
		for k, v := range labels {
			n.Labels[k] = asString(v)
		}
	}
	if t := parseTime(asString(row["registered_at"])); !t.IsZero() {
		n.RegisteredAt = t
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// FreeSlotsElsewhere sums free capacity on ready nodes excluding excludeID.
func FreeSlotsElsewhere(nodes []FleetNode, excludeID string) int {
	total := 0
	for _, n := range nodes {
		if n.ID == excludeID || !n.IsReady() {
			continue
		}
		free := n.CapacitySlots - n.EffectiveAllocatedSlots()
		if free > 0 {
			total += free
		}
	}
	return total
}

// ScaleDownWindowID is a stable idempotency key for a pool + victim node window.
func ScaleDownWindowID(pool, nodeID string) string {
	raw := pool + "|" + nodeID
	sum := uint32(2166136261)
	for i := 0; i < len(raw); i++ {
		sum ^= uint32(raw[i])
		sum *= 16777619
	}
	return fmt.Sprintf("scaledown-%s-%08x", sanitizeName(pool), sum)
}
