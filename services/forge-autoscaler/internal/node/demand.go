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

// PendingWorkload is one unschedulable placement from Control.
type PendingWorkload struct {
	PlacementID  string
	DeploymentID string
	ReplicaIndex int
	Reason       string
	Slots        int
	ServiceID    string
	// Optional demand constraints (from reason labels / future 25.01 fields).
	Region       string
	Architecture string
	GPU          int
	Labels       map[string]string
}

// ClusterReservation summarises fleet capacity vs allocation.
type ClusterReservation struct {
	Nodes           int
	CapacitySlots   int
	AllocatedSlots  int
	CapacityCPU     int
	AllocatedCPU    int
	CapacityMemory  int
	AllocatedMemory int
	CapacityGPU     int
	AllocatedGPU    int
}

// Ratio returns the highest reservation fraction among known dimensions (0–1).
func (c ClusterReservation) Ratio() float64 {
	var ratios []float64
	if c.CapacitySlots > 0 {
		ratios = append(ratios, float64(c.AllocatedSlots)/float64(c.CapacitySlots))
	}
	if c.CapacityCPU > 0 {
		ratios = append(ratios, float64(c.AllocatedCPU)/float64(c.CapacityCPU))
	}
	if c.CapacityMemory > 0 {
		ratios = append(ratios, float64(c.AllocatedMemory)/float64(c.CapacityMemory))
	}
	if c.CapacityGPU > 0 {
		ratios = append(ratios, float64(c.AllocatedGPU)/float64(c.CapacityGPU))
	}
	max := 0.0
	for _, r := range ratios {
		if r > max {
			max = r
		}
	}
	return max
}

// SignalSource loads pending placements and cluster reservation from Control.
type SignalSource struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (s *SignalSource) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// ListPending returns cluster-wide pending placements (status=pending, no deployment filter).
func (s *SignalSource) ListPending(ctx context.Context) ([]PendingWorkload, error) {
	if strings.TrimSpace(s.BaseURL) == "" {
		return nil, fmt.Errorf("control URL is not configured")
	}
	endpoint := strings.TrimRight(s.BaseURL, "/") + "/v1/placements?status=pending"
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
		return nil, fmt.Errorf("list pending placements: status %d: %s", resp.StatusCode, truncate(body))
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode placements: %w", err)
	}
	out := make([]PendingWorkload, 0, len(rows))
	for _, row := range rows {
		w := PendingWorkload{
			PlacementID:  asString(row["placement_id"]),
			DeploymentID: asString(row["deployment_id"]),
			Reason:       asString(row["reason"]),
			ServiceID:    asString(row["service_id"]),
			Labels:       map[string]string{},
		}
		if n, ok := asInt(row["replica_index"]); ok {
			w.ReplicaIndex = n
		}
		if n, ok := asInt(row["slots"]); ok && n > 0 {
			w.Slots = n
		} else {
			w.Slots = 1
		}
		// Soft-parse constraints from reason text until 25.01 structured fields land.
		parseDemandHints(&w)
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PlacementID == out[j].PlacementID {
			return out[i].ReplicaIndex < out[j].ReplicaIndex
		}
		return out[i].PlacementID < out[j].PlacementID
	})
	return out, nil
}

// ClusterReservation loads fleet nodes and aggregates capacity/allocation.
func (s *SignalSource) ClusterReservation(ctx context.Context) (ClusterReservation, error) {
	if strings.TrimSpace(s.BaseURL) == "" {
		return ClusterReservation{}, fmt.Errorf("control URL is not configured")
	}
	endpoint := strings.TrimRight(s.BaseURL, "/") + "/v1/nodes"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ClusterReservation{}, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return ClusterReservation{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return ClusterReservation{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ClusterReservation{}, fmt.Errorf("list nodes: status %d: %s", resp.StatusCode, truncate(body))
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return ClusterReservation{}, fmt.Errorf("decode nodes: %w", err)
	}
	var r ClusterReservation
	for _, row := range rows {
		status := strings.ToLower(asString(row["status"]))
		if status != "" && status != "online" && status != "ready" {
			continue
		}
		r.Nodes++
		if cap, ok := row["capacity"].(map[string]any); ok {
			if n, ok := asInt(cap["slots"]); ok {
				r.CapacitySlots += n
			}
			if n, ok := asInt(cap["cpu_millis"]); ok {
				r.CapacityCPU += n
			}
			if n, ok := asInt(cap["mem_mb"]); ok {
				r.CapacityMemory += n
			}
			if n, ok := asInt(cap["gpu"]); ok {
				r.CapacityGPU += n
			}
		}
		if alloc, ok := row["allocated"].(map[string]any); ok {
			if n, ok := asInt(alloc["slots"]); ok {
				r.AllocatedSlots += n
			}
			if n, ok := asInt(alloc["cpu_millis"]); ok {
				r.AllocatedCPU += n
			}
			if n, ok := asInt(alloc["mem_mb"]); ok {
				r.AllocatedMemory += n
			}
			if n, ok := asInt(alloc["gpu"]); ok {
				r.AllocatedGPU += n
			}
		}
	}
	return r, nil
}

func parseDemandHints(w *PendingWorkload) {
	reason := strings.ToLower(w.Reason)
	if strings.Contains(reason, "gpu") || strings.Contains(reason, "insufficientgpu") {
		if w.GPU == 0 {
			w.GPU = 1
		}
	}
	if strings.Contains(reason, "arm64") {
		w.Architecture = "arm64"
	} else if strings.Contains(reason, "amd64") || strings.Contains(reason, "x86_64") {
		w.Architecture = "amd64"
	}
}

// DemandWindowID is a stable idempotency key for a set of pending placements + pool.
func DemandWindowID(pool string, pending []PendingWorkload) string {
	ids := make([]string, 0, len(pending))
	for _, p := range pending {
		if p.PlacementID != "" {
			ids = append(ids, p.PlacementID)
		} else {
			ids = append(ids, fmt.Sprintf("%s#%d", p.DeploymentID, p.ReplicaIndex))
		}
	}
	sort.Strings(ids)
	raw := pool + "|" + strings.Join(ids, ",")
	sum := uint32(2166136261)
	for i := 0; i < len(raw); i++ {
		sum ^= uint32(raw[i])
		sum *= 16777619
	}
	return fmt.Sprintf("scaleup-%s-%08x", sanitizeName(pool), sum)
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "pool"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		return int(i), err == nil
	case string:
		var n int
		_, err := fmt.Sscanf(t, "%d", &n)
		return n, err == nil
	default:
		return 0, false
	}
}

func truncate(b []byte) string {
	const max = 256
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// PendingSlots sums slot demand across pending workloads.
func PendingSlots(pending []PendingWorkload) int {
	total := 0
	for _, p := range pending {
		if p.Slots > 0 {
			total += p.Slots
		} else {
			total++
		}
	}
	return total
}
