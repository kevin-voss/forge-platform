package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PeerCaller reaches fulfillment/notify via Discovery Ready endpoints.
type PeerCaller interface {
	Fulfill(ctx context.Context, orderID string) error
	Notify(ctx context.Context, orderID, email string) error
}

// discoveryPeers resolves *.svc.forge peer URLs through forge-discovery
// (Ready-only endpoint list) then dials the selected address.
type discoveryPeers struct {
	cfg    peerConfig
	client *http.Client
}

func newDiscoveryPeers(cfg peerConfig) *discoveryPeers {
	return &discoveryPeers{
		cfg: cfg,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *discoveryPeers) Fulfill(ctx context.Context, orderID string) error {
	body := map[string]string{"orderId": orderID}
	return p.postPeer(ctx, p.cfg.FulfillmentURL, "/fulfill", body, http.StatusAccepted)
}

func (p *discoveryPeers) Notify(ctx context.Context, orderID, email string) error {
	body := map[string]any{
		"orderId": orderID,
		"channel": "email",
		"message": fmt.Sprintf("Order %s placed for %s", orderID, email),
	}
	return p.postPeer(ctx, p.cfg.NotifyURL, "/notify", body, http.StatusAccepted)
}

func (p *discoveryPeers) postPeer(ctx context.Context, peerURL, path string, body any, wantStatus int) error {
	base, err := url.Parse(strings.TrimSpace(peerURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("invalid peer url %q: %w", peerURL, err)
	}
	service, err := serviceFromDiscoveryHost(base.Hostname())
	if err != nil {
		return err
	}
	addr, err := p.resolveReady(ctx, service)
	if err != nil {
		return fmt.Errorf("resolve %s via discovery: %w", service, err)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(addr.IP, fmt.Sprintf("%d", addr.Port)),
		Path:   path,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = base.Host

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", target.String(), err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("POST %s: status %d, want %d", target.String(), resp.StatusCode, wantStatus)
	}
	return nil
}

type discoveryAddress struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type discoveryEndpoint struct {
	Ready   bool             `json:"ready"`
	Phase   string           `json:"phase"`
	Address discoveryAddress `json:"address"`
}

func (p *discoveryPeers) resolveReady(ctx context.Context, service string) (discoveryAddress, error) {
	base := strings.TrimRight(strings.TrimSpace(p.cfg.DiscoveryURL), "/")
	if base == "" {
		return discoveryAddress{}, fmt.Errorf("FORGE_DISCOVERY_URL is required")
	}
	u := fmt.Sprintf("%s/v1/projects/%s/environments/%s/services/%s/endpoints",
		base,
		url.PathEscape(p.cfg.Project),
		url.PathEscape(p.cfg.Environment),
		url.PathEscape(service),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return discoveryAddress{}, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return discoveryAddress{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return discoveryAddress{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return discoveryAddress{}, fmt.Errorf("list endpoints status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var eps []discoveryEndpoint
	if err := json.Unmarshal(raw, &eps); err != nil {
		return discoveryAddress{}, fmt.Errorf("decode endpoints: %w", err)
	}
	for _, ep := range eps {
		if !ep.Ready && !strings.EqualFold(ep.Phase, "Ready") {
			continue
		}
		if strings.TrimSpace(ep.Address.IP) == "" || ep.Address.Port <= 0 {
			continue
		}
		return ep.Address, nil
	}
	return discoveryAddress{}, fmt.Errorf("no Ready endpoints for service %q", service)
}

// serviceFromDiscoveryHost extracts the service label from a Discovery hostname.
// Accepts short form "fulfillment.svc.forge" and FQDN
// "fulfillment.local.orderpipe.svc.forge".
func serviceFromDiscoveryHost(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", fmt.Errorf("empty peer host")
	}
	if !strings.HasSuffix(host, ".svc.forge") {
		return "", fmt.Errorf("peer host %q must be a *.svc.forge Discovery name", host)
	}
	prefix := strings.TrimSuffix(host, ".svc.forge")
	labels := strings.Split(prefix, ".")
	if len(labels) == 0 || labels[0] == "" {
		return "", fmt.Errorf("peer host %q missing service label", host)
	}
	return labels[0], nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
