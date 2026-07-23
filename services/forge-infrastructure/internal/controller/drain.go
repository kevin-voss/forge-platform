package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DrainHook stops new placements and reschedules workloads off a draining node (epic 08).
type DrainHook interface {
	BeginDrain(ctx context.Context, runtimeNodeID string) error
	Workloads(ctx context.Context, runtimeNodeID string) ([]string, error)
}

// MemoryDrain is a test double that tracks drain state and workloads.
type MemoryDrain struct {
	mu         sync.Mutex
	Draining   map[string]bool
	WorkloadsM map[string][]string
	BeginCalls int
}

// NewMemoryDrain returns an empty drain hook.
func NewMemoryDrain() *MemoryDrain {
	return &MemoryDrain{
		Draining:   map[string]bool{},
		WorkloadsM: map[string][]string{},
	}
}

func (m *MemoryDrain) BeginDrain(ctx context.Context, runtimeNodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BeginCalls++
	m.Draining[runtimeNodeID] = true
	return nil
}

func (m *MemoryDrain) Workloads(ctx context.Context, runtimeNodeID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.WorkloadsM[runtimeNodeID]...), nil
}

// SetWorkloads sets the workload ids currently on a node (tests).
func (m *MemoryDrain) SetWorkloads(runtimeNodeID string, ids []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WorkloadsM[runtimeNodeID] = append([]string{}, ids...)
}

// IsDraining reports whether BeginDrain was called for the node.
func (m *MemoryDrain) IsDraining(runtimeNodeID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Draining[runtimeNodeID]
}

// CanPlace returns false when the node is draining (scheduler placement gate).
func (m *MemoryDrain) CanPlace(runtimeNodeID string) bool {
	return !m.IsDraining(runtimeNodeID)
}

// ControlDrainHook observes Control's fleet API for workload counts.
// BeginDrain records intent locally and best-effort notifies Control; epic 08
// treats offline/draining nodes as unschedulable once liveness flips.
type ControlDrainHook struct {
	ControlURL string
	HTTPClient *http.Client
	mu         sync.Mutex
	started    map[string]time.Time
}

// NewControlDrainHook constructs a Control-backed drain hook.
func NewControlDrainHook(controlURL string) *ControlDrainHook {
	return &ControlDrainHook{
		ControlURL: strings.TrimRight(controlURL, "/"),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		started:    map[string]time.Time{},
	}
}

func (c *ControlDrainHook) BeginDrain(ctx context.Context, runtimeNodeID string) error {
	c.mu.Lock()
	c.started[runtimeNodeID] = time.Now().UTC()
	c.mu.Unlock()
	// Best-effort: POST offline signal if Control exposes it; ignore 404.
	if c.ControlURL == "" || runtimeNodeID == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ControlURL+"/v1/nodes/"+runtimeNodeID+"/offline", nil)
	if err != nil {
		return nil
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}

func (c *ControlDrainHook) Workloads(ctx context.Context, runtimeNodeID string) ([]string, error) {
	if c.ControlURL == "" || runtimeNodeID == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ControlURL+"/v1/nodes", nil)
	if err != nil {
		return nil, err
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list fleet nodes: status %d", resp.StatusCode)
	}
	var nodes []fleetNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, err
	}
	for _, n := range nodes {
		id := n.ID
		if id == "" {
			id = n.NodeID
		}
		if id == runtimeNodeID {
			return append([]string{}, n.RunningReplicas...), nil
		}
	}
	return nil, nil
}

type fleetNode struct {
	ID              string   `json:"id"`
	NodeID          string   `json:"node_id"`
	Address         string   `json:"address"`
	Status          string   `json:"status"`
	RunningReplicas []string `json:"running_replicas"`
}
