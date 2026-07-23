package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// DriftMetrics holds Prometheus counters for route/DNS drift (22.06).
type DriftMetrics struct {
	RouteDriftTotal     atomic.Int64
	DNSResolutionOK     atomic.Int64
	DNSResolutionError  atomic.Int64
	DNSResolutionNXDom  atomic.Int64
}

// AddRouteDrift increments the route drift counter by n (n >= 1).
func (m *DriftMetrics) AddRouteDrift(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.RouteDriftTotal.Add(n)
}

// RecordDNSResolution increments forge_network_dns_resolution_total{result}.
func (m *DriftMetrics) RecordDNSResolution(result string) {
	if m == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "ok":
		m.DNSResolutionOK.Add(1)
	case "nxdomain":
		m.DNSResolutionNXDom.Add(1)
	default:
		m.DNSResolutionError.Add(1)
	}
}

// DriftItem is one mismatched Discovery endpoint vs Network lease / route.
type DriftItem struct {
	EndpointID        string `json:"endpoint_id"`
	ExpectedOverlayIP string `json:"expected_overlay_ip"`
	ObservedRoute     string `json:"observed_route"`
}

// DiscoveryEndpoint is a Ready endpoint pulled from Discovery.
type DiscoveryEndpoint struct {
	ID        string
	AddressIP string
	Service   string
	Project   string
	Env       string
}

// DiscoveryClient lists services + Ready endpoints from forge-discovery.
type DiscoveryClient struct {
	BaseURL string
	HTTP    *http.Client
	Log     *slog.Logger
}

func (c *DiscoveryClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// ListReadyEndpoints walks GET /v1/services then per-service Ready endpoints.
func (c *DiscoveryClient) ListReadyEndpoints(ctx context.Context) ([]DiscoveryEndpoint, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return nil, nil
	}
	base := strings.TrimRight(c.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/services", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("list services HTTP %d: %s", resp.StatusCode, string(b))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var services []serviceRow
	if err := json.Unmarshal(body, &services); err != nil {
		var wrap struct {
			Items    []serviceRow `json:"items"`
			Services []serviceRow `json:"services"`
		}
		if err2 := json.Unmarshal(body, &wrap); err2 != nil {
			return nil, err
		}
		services = wrap.Items
		if len(services) == 0 {
			services = wrap.Services
		}
	}
	var out []DiscoveryEndpoint
	for _, svc := range services {
		eps, err := c.listServiceEndpoints(ctx, base, svc)
		if err != nil {
			if c.Log != nil {
				c.Log.Warn("drift: list endpoints failed", "service", svc.Name, "error", err.Error())
			}
			continue
		}
		out = append(out, eps...)
	}
	return out, nil
}

type serviceRow struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Name        string `json:"name"`
}

func (c *DiscoveryClient) listServiceEndpoints(ctx context.Context, base string, svc serviceRow) ([]DiscoveryEndpoint, error) {
	url := fmt.Sprintf("%s/v1/projects/%s/environments/%s/services/%s/endpoints",
		base, pathSeg(svc.Project), pathSeg(svc.Environment), pathSeg(svc.Name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var rows []endpointRow
	if err := json.Unmarshal(body, &rows); err != nil {
		var wrap struct {
			Items     []endpointRow `json:"items"`
			Endpoints []endpointRow `json:"endpoints"`
		}
		if err2 := json.Unmarshal(body, &wrap); err2 != nil {
			return nil, err
		}
		rows = wrap.Items
		if len(rows) == 0 {
			rows = wrap.Endpoints
		}
	}
	var out []DiscoveryEndpoint
	for _, ep := range rows {
		phase := ep.Phase
		if phase == "" {
			phase = "Ready"
		}
		if phase != "Ready" {
			continue
		}
		ip := ep.AddressIP
		if ip == "" && ep.Address != nil {
			ip = ep.Address.IP
		}
		if ip == "" {
			continue
		}
		out = append(out, DiscoveryEndpoint{
			ID: ep.ID, AddressIP: ip, Service: svc.Name, Project: svc.Project, Env: svc.Environment,
		})
	}
	return out, nil
}

type endpointRow struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	AddressIP string `json:"addressIp"`
	Address   *struct {
		IP string `json:"ip"`
	} `json:"address"`
}

func pathSeg(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "/", "")
}

// IsProviderPublicIP reports whether addr is a non-RFC1918 / non-loopback IPv4.
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

// InOverlayCIDR reports whether addr is inside cidr.
func InOverlayCIDR(addr, cidr string) bool {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return false
	}
	_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	return n.Contains(ip)
}

// Reconciler compares Discovery Ready endpoints to active Network leases (22.06).
type Reconciler struct {
	Alloc        *Allocator
	Discovery    *DiscoveryClient
	NetworkName  string
	OverlayCIDR  string
	Metrics      *DriftMetrics
	Log          *slog.Logger
	Interval     time.Duration
}

// ReconcileOnce runs one drift comparison pass.
func (r *Reconciler) ReconcileOnce(ctx context.Context) ([]DriftItem, error) {
	if r == nil || r.Alloc == nil {
		return nil, fmt.Errorf("reconciler not configured")
	}
	leases, err := r.Alloc.ListActiveWorkloadLeases(ctx, r.NetworkName)
	if err != nil {
		return nil, err
	}
	byID := map[string]ActiveWorkloadLease{}
	byAddr := map[string]ActiveWorkloadLease{}
	for _, l := range leases {
		byID[l.WorkloadID] = l
		byAddr[l.Address] = l
	}
	var endpoints []DiscoveryEndpoint
	if r.Discovery != nil {
		endpoints, err = r.Discovery.ListReadyEndpoints(ctx)
		if err != nil {
			return nil, err
		}
	}
	cidr := r.OverlayCIDR
	if cidr == "" {
		cidr = "10.100.0.0/16"
	}
	var drifted []DriftItem
	for _, ep := range endpoints {
		if IsProviderPublicIP(ep.AddressIP) || !InOverlayCIDR(ep.AddressIP, cidr) {
			drifted = append(drifted, DriftItem{
				EndpointID:        ep.ID,
				ExpectedOverlayIP: "",
				ObservedRoute:     "public_or_non_overlay=" + ep.AddressIP,
			})
			if r.Metrics != nil {
				r.Metrics.RecordDNSResolution("error")
			}
			continue
		}
		lease, ok := byID[ep.ID]
		if !ok {
			// Address may still match a lease under a different key — require exact lease.
			if _, addrOK := byAddr[ep.AddressIP]; !addrOK {
				drifted = append(drifted, DriftItem{
					EndpointID:        ep.ID,
					ExpectedOverlayIP: "",
					ObservedRoute:     "discovery=" + ep.AddressIP,
				})
				continue
			}
			lease = byAddr[ep.AddressIP]
		}
		if lease.Address != ep.AddressIP {
			drifted = append(drifted, DriftItem{
				EndpointID:        ep.ID,
				ExpectedOverlayIP: lease.Address,
				ObservedRoute:     "discovery=" + ep.AddressIP,
			})
			continue
		}
		if r.Metrics != nil {
			r.Metrics.RecordDNSResolution("ok")
		}
	}
	if len(drifted) > 0 {
		if r.Metrics != nil {
			r.Metrics.AddRouteDrift(int64(len(drifted)))
		}
		for _, d := range drifted {
			if r.Log != nil {
				r.Log.Warn("network route drift",
					"event", "network.route.drift",
					"endpoint_id", d.EndpointID,
					"expected_overlay_ip", d.ExpectedOverlayIP,
					"observed_route", d.ObservedRoute,
				)
			}
		}
	}
	return drifted, nil
}

// Run polls until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	every := r.Interval
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := r.ReconcileOnce(ctx); err != nil && r.Log != nil {
				r.Log.Warn("drift reconcile failed", "error", err.Error())
			}
		}
	}
}
