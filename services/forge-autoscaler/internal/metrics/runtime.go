package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/policy"
)

// RuntimeSource is a degraded local-mode fallback that estimates CPU/memory
// utilization from forge-runtime node facts and workload count when Observe
// is unavailable.
type RuntimeSource struct {
	BaseURL    string
	HTTPClient *http.Client
}

type runtimeNode struct {
	CPU         uint64 `json:"cpu"`
	MemoryBytes uint64 `json:"memoryBytes"`
}

type runtimeNodeState struct {
	Workloads []any `json:"workloads"`
}

// Fetch implements MetricSource.
func (s *RuntimeSource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	if strings.TrimSpace(s.BaseURL) == "" {
		return Sample{Source: "runtime"}, fmt.Errorf("%w: runtime URL empty", ErrNotImplemented)
	}
	kind := strings.ToLower(strings.TrimSpace(metric.Type))
	if kind != "cpu" && kind != "memory" {
		return Sample{Source: "runtime"}, fmt.Errorf("%w: runtime fallback only supports cpu/memory", ErrNotImplemented)
	}

	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	node, err := s.getJSON(ctx, client, "/v1/node")
	if err != nil {
		return Sample{Source: "runtime"}, err
	}
	var nodeInfo runtimeNode
	if err := json.Unmarshal(node, &nodeInfo); err != nil {
		return Sample{Source: "runtime"}, err
	}
	stateRaw, err := s.getJSON(ctx, client, "/v1/node/state")
	if err != nil {
		return Sample{Source: "runtime"}, err
	}
	var state runtimeNodeState
	if err := json.Unmarshal(stateRaw, &state); err != nil {
		return Sample{Source: "runtime"}, err
	}

	util := estimateUtilization(kind, nodeInfo, len(state.Workloads), target)
	return Sample{
		Value:      util,
		Target:     TargetAverage(metric),
		ObservedAt: time.Now().UTC(),
		Source:     "runtime",
	}, nil
}

func (s *RuntimeSource) getJSON(ctx context.Context, client *http.Client, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("runtime %s status %d", path, resp.StatusCode)
	}
	return body, nil
}

// estimateUtilization maps node capacity + workload count into a 0–100 style
// utilization percentage. This is intentionally coarse — Observe is authoritative.
func estimateUtilization(kind string, node runtimeNode, workloadCount int, _ policy.TargetRef) float64 {
	if workloadCount < 0 {
		workloadCount = 0
	}
	switch kind {
	case "memory":
		// Assume ~256Mi per workload against node memory.
		if node.MemoryBytes == 0 {
			return float64(workloadCount) * 25
		}
		used := float64(workloadCount) * 256 * 1024 * 1024
		pct := used / float64(node.MemoryBytes) * 100
		if pct > 100 {
			return 100
		}
		return pct
	default: // cpu
		cpus := float64(node.CPU)
		if cpus < 1 {
			cpus = 1
		}
		// Assume each workload consumes ~0.5 CPU core on average.
		pct := (float64(workloadCount) * 0.5 / cpus) * 100
		if pct > 100 {
			return 100
		}
		return pct
	}
}
