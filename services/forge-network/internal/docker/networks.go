package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client talks to Docker Engine over DOCKER_HOST.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// New creates a Docker Engine client.
func New(dockerHost string) (*Client, error) {
	transport, baseURL, err := dialerForHost(dockerHost)
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
		baseURL: baseURL,
	}, nil
}

func dialerForHost(dockerHost string) (*http.Transport, string, error) {
	host := strings.TrimSpace(dockerHost)
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	switch {
	case strings.HasPrefix(host, "unix://"):
		socket := strings.TrimPrefix(host, "unix://")
		if socket == "" {
			return nil, "", fmt.Errorf("DOCKER_HOST unix path is empty")
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		}
		return transport, "http://localhost", nil
	case strings.HasPrefix(host, "tcp://"):
		addr := strings.TrimPrefix(host, "tcp://")
		if addr == "" {
			return nil, "", fmt.Errorf("DOCKER_HOST tcp address is empty")
		}
		return &http.Transport{}, "http://" + addr, nil
	case strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://"):
		return &http.Transport{}, host, nil
	default:
		return nil, "", fmt.Errorf("unsupported DOCKER_HOST scheme in %q", dockerHost)
	}
}

type networkListItem struct {
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	IPAM   networkIPAM       `json:"IPAM"`
	Scope  string            `json:"Scope"`
	Labels map[string]string `json:"Labels"`
}

type networkIPAM struct {
	Config []networkIPAMConfig `json:"Config"`
}

type networkIPAMConfig struct {
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
}

// BridgeSubnets returns IPv4 subnets from Docker networks (bridge and custom).
func (c *Client) BridgeSubnets(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/networks", nil)
	if err != nil {
		return nil, fmt.Errorf("docker networks request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker networks: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("docker networks read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker networks: unexpected status %d", resp.StatusCode)
	}
	var items []networkListItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("docker networks decode: %w", err)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		for _, cfg := range item.IPAM.Config {
			subnet := strings.TrimSpace(cfg.Subnet)
			if subnet == "" {
				continue
			}
			ip, _, err := net.ParseCIDR(subnet)
			if err != nil || ip == nil || ip.To4() == nil {
				continue
			}
			if _, ok := seen[subnet]; ok {
				continue
			}
			seen[subnet] = struct{}{}
			out = append(out, subnet)
		}
	}
	return out, nil
}
