package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"forge.local/services/forge-infrastructure/internal/provider"
)

// MachineObserver reports whether a provider machine exists and is booted.
type MachineObserver interface {
	IsBooted(ctx context.Context, prov provider.Provider, providerNodeID string) (bool, error)
}

// ProviderMachineObserver uses Provider.GetNode.
type ProviderMachineObserver struct{}

func (ProviderMachineObserver) IsBooted(ctx context.Context, prov provider.Provider, providerNodeID string) (bool, error) {
	if prov == nil || providerNodeID == "" {
		return false, nil
	}
	n, err := prov.GetNode(ctx, providerNodeID)
	if err != nil {
		return false, nil // not booted yet / gone
	}
	if n == nil {
		return false, nil
	}
	phase := strings.ToLower(n.Phase)
	switch phase {
	case "ready", "running", "bootstrapping", "joining", "":
		// empty phase with an id means the machine exists
		return true, nil
	case "provisioning", "pending", "creating":
		return false, nil
	default:
		return true, nil
	}
}

// HealthProber checks Runtime readiness on a node address.
type HealthProber interface {
	Ready(ctx context.Context, address string) (bool, error)
}

// HTTPHealthProber GETs {address}/health/ready.
type HTTPHealthProber struct {
	HTTPClient *http.Client
}

func (h *HTTPHealthProber) Ready(ctx context.Context, address string) (bool, error) {
	url := healthURL(address)
	if url == "" {
		return false, nil
	}
	client := h.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode >= 200 && resp.StatusCode < 300, nil
}

func healthURL(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		address = "http://" + address
	}
	return strings.TrimRight(address, "/") + "/health/ready"
}

// JoinObserver detects 04.02 registration + heartbeat via Control fleet.
type JoinObserver interface {
	Observe(ctx context.Context, address string) (runtimeNodeID string, online bool, err error)
}

// ControlJoinObserver matches fleet nodes by address.
type ControlJoinObserver struct {
	ControlURL string
	HTTPClient *http.Client
}

func (c *ControlJoinObserver) Observe(ctx context.Context, address string) (string, bool, error) {
	if c.ControlURL == "" {
		return "", false, nil
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.ControlURL, "/")+"/v1/nodes", nil)
	if err != nil {
		return "", false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("fleet list status %d", resp.StatusCode)
	}
	var nodes []fleetNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return "", false, err
	}
	want := normalizeAddr(address)
	for _, n := range nodes {
		if normalizeAddr(n.Address) == want || (want != "" && strings.Contains(normalizeAddr(n.Address), want)) {
			id := n.ID
			if id == "" {
				id = n.NodeID
			}
			status := strings.ToLower(n.Status)
			online := status == "online" || status == "joining" || status == "pending-network"
			return id, online || status == "online", nil
		}
	}
	return "", false, nil
}

func normalizeAddr(a string) string {
	a = strings.TrimSpace(strings.ToLower(a))
	a = strings.TrimPrefix(a, "http://")
	a = strings.TrimPrefix(a, "https://")
	return strings.TrimRight(a, "/")
}

// StaticJoinObserver returns a fixed observation (tests).
type StaticJoinObserver struct {
	RuntimeNodeID string
	Online        bool
}

func (s StaticJoinObserver) Observe(ctx context.Context, address string) (string, bool, error) {
	return s.RuntimeNodeID, s.Online, nil
}

// StaticHealth always returns the configured readiness.
type StaticHealth struct {
	OK bool
}

func (s StaticHealth) Ready(ctx context.Context, address string) (bool, error) {
	return s.OK, nil
}

// StaticMachine reports a fixed booted state.
type StaticMachine struct {
	Booted bool
}

func (s StaticMachine) IsBooted(ctx context.Context, prov provider.Provider, providerNodeID string) (bool, error) {
	return s.Booted, nil
}
