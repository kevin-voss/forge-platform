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

// Engine is the subset of Docker Engine operations used by forge-build.
type Engine interface {
	Ping(ctx context.Context) error
	ServerVersion(ctx context.Context) (string, error)
	Close() error
}

// Client talks to Docker Engine over DOCKER_HOST (unix socket or TCP).
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// New creates a Docker Engine client for dockerHost (e.g. unix:///var/run/docker.sock).
func New(dockerHost string) (*Client, error) {
	transport, baseURL, err := dialerForHost(dockerHost)
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
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
		// Host is ignored for unix dial; Docker API expects http://localhost/...
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
		return nil, "", fmt.Errorf("unsupported DOCKER_HOST scheme in %q (use unix:// or tcp://)", dockerHost)
	}
}

// Ping verifies the Engine is reachable via GET /_ping.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/_ping", nil)
	if err != nil {
		return fmt.Errorf("docker ping request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping: unexpected status %d", resp.StatusCode)
	}
	return nil
}

type versionResponse struct {
	Version string `json:"Version"`
}

// ServerVersion returns the Engine version string via GET /version.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/version", nil)
	if err != nil {
		return "", fmt.Errorf("docker version request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker version: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("docker version read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docker version: unexpected status %d", resp.StatusCode)
	}
	var payload versionResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("docker version decode: %w", err)
	}
	if payload.Version == "" {
		return "", fmt.Errorf("docker version: empty Version field")
	}
	return payload.Version, nil
}

// Close releases idle connections.
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// StartupPing retries Ping with a bounded attempt count.
// On success it returns the Engine version. On exhaustion it returns the last error.
func StartupPing(ctx context.Context, engine Engine, retries int, delay time.Duration) (string, error) {
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && delay > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := engine.Ping(pingCtx)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		verCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		version, err := engine.ServerVersion(verCtx)
		cancel()
		if err != nil {
			return "", err
		}
		return version, nil
	}
	return "", fmt.Errorf("docker unreachable after %d attempts: %w", attempts, lastErr)
}
