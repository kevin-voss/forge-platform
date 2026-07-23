package dns

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OverlayFilter decides whether a Ready endpoint address may appear in DNS answers.
type OverlayFilter interface {
	Allow(ctx context.Context, endpointID, addressIP string) bool
}

// PublicIPRejectFilter rejects provider public IPs; private/Docker IPs still pass
// (used when forge-network is not wired so epic 21 demos keep working).
type PublicIPRejectFilter struct{}

// Allow implements OverlayFilter.
func (PublicIPRejectFilter) Allow(_ context.Context, _, addressIP string) bool {
	return !IsProviderPublicIP(addressIP)
}

// CIDROverlayFilter allows only addresses inside OverlayCIDR and rejects public IPs.
// When LeaseChecker is set, the address must also have a current Network lease.
type CIDROverlayFilter struct {
	OverlayCIDR  *net.IPNet
	LeaseChecker LeaseChecker
}

// LeaseChecker reports whether an overlay address (or endpoint id) has an active lease.
type LeaseChecker interface {
	HasLease(ctx context.Context, endpointID, addressIP string) bool
}

// Allow implements OverlayFilter.
func (f *CIDROverlayFilter) Allow(ctx context.Context, endpointID, addressIP string) bool {
	if f == nil {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(addressIP))
	if ip == nil {
		return false
	}
	if IsProviderPublicIP(addressIP) {
		return false
	}
	if f.OverlayCIDR != nil && !f.OverlayCIDR.Contains(ip) {
		return false
	}
	if f.LeaseChecker != nil {
		return f.LeaseChecker.HasLease(ctx, endpointID, addressIP)
	}
	return true
}

// IsProviderPublicIP reports non-RFC1918 / non-loopback IPv4 addresses.
func IsProviderPublicIP(addr string) bool {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return true
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return !strings.HasPrefix(strings.ToLower(addr), "fd")
	}
	if ip4[0] == 10 {
		return false
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return false
	}
	if ip4[0] == 192 && ip4[1] == 168 {
		return false
	}
	if ip4[0] == 127 || (ip4[0] == 169 && ip4[1] == 254) {
		return false
	}
	if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return false
	}
	return true
}

// NetworkLeaseIndex caches active workload leases from forge-network.
type NetworkLeaseIndex struct {
	BaseURL     string
	NetworkName string
	HTTP        *http.Client
	RefreshEvery time.Duration

	mu      sync.RWMutex
	byID    map[string]string
	byAddr  map[string]struct{}
	lastErr error
}

// HasLease implements LeaseChecker.
func (i *NetworkLeaseIndex) HasLease(_ context.Context, endpointID, addressIP string) bool {
	if i == nil {
		return true
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	if len(i.byAddr) == 0 && len(i.byID) == 0 {
		// Index not yet populated — fall back to CIDR-only (caller already checked CIDR).
		return true
	}
	if addressIP != "" {
		if _, ok := i.byAddr[addressIP]; ok {
			return true
		}
	}
	if endpointID != "" {
		if addr, ok := i.byID[endpointID]; ok && (addressIP == "" || addr == addressIP) {
			return true
		}
	}
	return false
}

// Refresh pulls GET /v1/networks/{name}/workload-leases.
func (i *NetworkLeaseIndex) Refresh(ctx context.Context) error {
	if i == nil || strings.TrimSpace(i.BaseURL) == "" {
		return nil
	}
	name := i.NetworkName
	if name == "" {
		name = "cluster-overlay"
	}
	client := i.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	url := strings.TrimRight(i.BaseURL, "/") + "/v1/networks/" + name + "/workload-leases"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		i.mu.Lock()
		i.lastErr = err
		i.mu.Unlock()
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return nil
	}
	var wrap struct {
		Leases []struct {
			WorkloadID string `json:"workload_id"`
			Address    string `json:"address"`
		} `json:"leases"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return err
	}
	byID := make(map[string]string, len(wrap.Leases))
	byAddr := make(map[string]struct{}, len(wrap.Leases))
	for _, l := range wrap.Leases {
		byID[l.WorkloadID] = l.Address
		byAddr[l.Address] = struct{}{}
	}
	i.mu.Lock()
	i.byID = byID
	i.byAddr = byAddr
	i.lastErr = nil
	i.mu.Unlock()
	return nil
}

// Run refreshes until ctx is cancelled.
func (i *NetworkLeaseIndex) Run(ctx context.Context) {
	every := i.RefreshEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	_ = i.Refresh(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = i.Refresh(ctx)
		}
	}
}

// ParseOverlayCIDR parses a CIDR string; empty → 10.100.0.0/16.
func ParseOverlayCIDR(raw string) (*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "10.100.0.0/16"
	}
	_, n, err := net.ParseCIDR(raw)
	return n, err
}
