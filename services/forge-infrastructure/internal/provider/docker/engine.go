package docker

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
)

// Engine is the Docker Engine surface used by the provider (faked in unit tests).
type Engine interface {
	Ping(ctx context.Context) error
	ContainerCreate(ctx context.Context, name string, cfg ContainerConfig) (string, error)
	ContainerStart(ctx context.Context, id string) error
	ContainerStop(ctx context.Context, id string, timeoutSec int) error
	ContainerRemove(ctx context.Context, id string, force bool) error
	ContainerRestart(ctx context.Context, id string, timeoutSec int) error
	ContainerInspect(ctx context.Context, id string) (*ContainerInspect, error)
	ContainerList(ctx context.Context, filters map[string][]string, all bool) ([]ContainerSummary, error)
	VolumeCreate(ctx context.Context, name string, labels map[string]string) (string, error)
	VolumeRemove(ctx context.Context, name string, force bool) error
	NetworkCreate(ctx context.Context, name string, labels map[string]string) (string, error)
	NetworkRemove(ctx context.Context, id string) error
	NetworkInspect(ctx context.Context, idOrName string) (*NetworkInspect, error)
	Close() error
}

// ContainerConfig is the create payload subset we need.
type ContainerConfig struct {
	Image       string
	Env         []string
	Labels      map[string]string
	Network     string
	Binds       []string // host:container
	NanoCPUs    int64
	MemoryBytes int64
	User        string   // e.g. "0:0" for Docker socket access
	GroupAdd    []string // supplementary groups (docker gid)
}

// ContainerInspect is a trimmed docker inspect response.
type ContainerInspect struct {
	ID              string
	Name            string
	State           ContainerState
	Config          InspectConfig
	NetworkSettings NetworkSettings
}

// ContainerState holds running state.
type ContainerState struct {
	Status  string
	Running bool
}

// InspectConfig holds labels/env from inspect.
type InspectConfig struct {
	Labels map[string]string
	Image  string
	Env    []string
}

// NetworkSettings holds IP addresses per network.
type NetworkSettings struct {
	IPAddress string
	Networks  map[string]EndpointSettings
}

// EndpointSettings is a per-network endpoint.
type EndpointSettings struct {
	IPAddress string
}

// ContainerSummary is a docker ps row.
type ContainerSummary struct {
	ID     string
	Names  []string
	Labels map[string]string
	State  string
	Status string
}

// NetworkInspect is a trimmed network inspect.
type NetworkInspect struct {
	ID     string
	Name   string
	Labels map[string]string
}

// Client is a light Docker Engine HTTP client (same approach as forge-build).
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient dials dockerHost (unix:///…, tcp://…, or a bare socket path).
func NewClient(dockerHost string) (*Client, error) {
	transport, baseURL, err := dialerForHost(dockerHost)
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			// Container create/start can be slow; per-call contexts bound work.
			Timeout: 0,
		},
		baseURL: baseURL,
	}, nil
}

func dialerForHost(dockerHost string) (*http.Transport, string, error) {
	host := strings.TrimSpace(dockerHost)
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	if !strings.Contains(host, "://") {
		host = "unix://" + host
	}
	switch {
	case strings.HasPrefix(host, "unix://"):
		socket := strings.TrimPrefix(host, "unix://")
		if socket == "" {
			return nil, "", fmt.Errorf("docker socket path is empty")
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
			return nil, "", fmt.Errorf("docker tcp address is empty")
		}
		return &http.Transport{}, "http://" + addr, nil
	case strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://"):
		return &http.Transport{}, host, nil
	default:
		return nil, "", fmt.Errorf("unsupported docker host %q", dockerHost)
	}
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/_ping", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) ContainerCreate(ctx context.Context, name string, cfg ContainerConfig) (string, error) {
	hostConfig := map[string]any{
		"Binds":       cfg.Binds,
		"NanoCpus":    cfg.NanoCPUs,
		"Memory":      cfg.MemoryBytes,
		"NetworkMode": cfg.Network,
	}
	if len(cfg.GroupAdd) > 0 {
		hostConfig["GroupAdd"] = cfg.GroupAdd
	}
	body := map[string]any{
		"Image":      cfg.Image,
		"Env":        cfg.Env,
		"Labels":     cfg.Labels,
		"HostConfig": hostConfig,
	}
	if strings.TrimSpace(cfg.User) != "" {
		body["User"] = cfg.User
	}
	var out struct {
		ID string `json:"Id"`
	}
	path := "/containers/create"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("docker create: empty id")
	}
	return out.ID, nil
}

func (c *Client) ContainerStart(ctx context.Context, id string) error {
	return c.doNoContent(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil)
}

func (c *Client) ContainerStop(ctx context.Context, id string, timeoutSec int) error {
	path := fmt.Sprintf("/containers/%s/stop?t=%d", url.PathEscape(id), timeoutSec)
	return c.doNoContent(ctx, http.MethodPost, path, nil)
}

func (c *Client) ContainerRemove(ctx context.Context, id string, force bool) error {
	path := fmt.Sprintf("/containers/%s?force=%v&v=true", url.PathEscape(id), force)
	return c.doNoContent(ctx, http.MethodDelete, path, nil)
}

func (c *Client) ContainerRestart(ctx context.Context, id string, timeoutSec int) error {
	path := fmt.Sprintf("/containers/%s/restart?t=%d", url.PathEscape(id), timeoutSec)
	return c.doNoContent(ctx, http.MethodPost, path, nil)
}

func (c *Client) ContainerInspect(ctx context.Context, id string) (*ContainerInspect, error) {
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &raw); err != nil {
		return nil, err
	}
	return parseInspect(raw), nil
}

func (c *Client) ContainerList(ctx context.Context, filters map[string][]string, all bool) ([]ContainerSummary, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	if len(filters) > 0 {
		b, _ := json.Marshal(filters)
		q.Set("filters", string(b))
	}
	path := "/containers/json"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var raw []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]ContainerSummary, 0, len(raw))
	for _, row := range raw {
		out = append(out, parseSummary(row))
	}
	return out, nil
}

func (c *Client) VolumeCreate(ctx context.Context, name string, labels map[string]string) (string, error) {
	body := map[string]any{"Name": name, "Labels": labels}
	var out struct {
		Name string `json:"Name"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/volumes/create", body, &out); err != nil {
		return "", err
	}
	if out.Name == "" {
		return name, nil
	}
	return out.Name, nil
}

func (c *Client) VolumeRemove(ctx context.Context, name string, force bool) error {
	path := fmt.Sprintf("/volumes/%s?force=%v", url.PathEscape(name), force)
	return c.doNoContent(ctx, http.MethodDelete, path, nil)
}

func (c *Client) NetworkCreate(ctx context.Context, name string, labels map[string]string) (string, error) {
	body := map[string]any{
		"Name":   name,
		"Driver": "bridge",
		"Labels": labels,
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/networks/create", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *Client) NetworkRemove(ctx context.Context, id string) error {
	return c.doNoContent(ctx, http.MethodDelete, "/networks/"+url.PathEscape(id), nil)
}

func (c *Client) NetworkInspect(ctx context.Context, idOrName string) (*NetworkInspect, error) {
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/networks/"+url.PathEscape(idOrName), nil, &raw); err != nil {
		return nil, err
	}
	ni := &NetworkInspect{}
	if v, ok := raw["Id"].(string); ok {
		ni.ID = v
	}
	if v, ok := raw["Name"].(string); ok {
		ni.Name = v
	}
	if labels, ok := raw["Labels"].(map[string]any); ok {
		ni.Labels = stringMap(labels)
	}
	return ni, nil
}

func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("docker %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("docker decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) doNoContent(ctx context.Context, method, path string, body any) error {
	err := c.doJSON(ctx, method, path, body, nil)
	if err == nil {
		return nil
	}
	// 304 / 404 on stop/remove are often idempotent enough for our callers.
	if strings.Contains(err.Error(), "status 304") || strings.Contains(err.Error(), "status 404") {
		return nil
	}
	return err
}

func parseInspect(raw map[string]any) *ContainerInspect {
	ci := &ContainerInspect{}
	if v, ok := raw["Id"].(string); ok {
		ci.ID = v
	}
	if v, ok := raw["Name"].(string); ok {
		ci.Name = strings.TrimPrefix(v, "/")
	}
	if state, ok := raw["State"].(map[string]any); ok {
		if v, ok := state["Status"].(string); ok {
			ci.State.Status = v
		}
		if v, ok := state["Running"].(bool); ok {
			ci.State.Running = v
		}
	}
	if cfg, ok := raw["Config"].(map[string]any); ok {
		if labels, ok := cfg["Labels"].(map[string]any); ok {
			ci.Config.Labels = stringMap(labels)
		}
		if v, ok := cfg["Image"].(string); ok {
			ci.Config.Image = v
		}
	}
	if ns, ok := raw["NetworkSettings"].(map[string]any); ok {
		if v, ok := ns["IPAddress"].(string); ok {
			ci.NetworkSettings.IPAddress = v
		}
		ci.NetworkSettings.Networks = map[string]EndpointSettings{}
		if nets, ok := ns["Networks"].(map[string]any); ok {
			for name, val := range nets {
				ep, _ := val.(map[string]any)
				ip, _ := ep["IPAddress"].(string)
				ci.NetworkSettings.Networks[name] = EndpointSettings{IPAddress: ip}
			}
		}
	}
	return ci
}

func parseSummary(row map[string]any) ContainerSummary {
	cs := ContainerSummary{Labels: map[string]string{}}
	if v, ok := row["Id"].(string); ok {
		cs.ID = v
	}
	if v, ok := row["State"].(string); ok {
		cs.State = v
	}
	if v, ok := row["Status"].(string); ok {
		cs.Status = v
	}
	if names, ok := row["Names"].([]any); ok {
		for _, n := range names {
			if s, ok := n.(string); ok {
				cs.Names = append(cs.Names, strings.TrimPrefix(s, "/"))
			}
		}
	}
	if labels, ok := row["Labels"].(map[string]any); ok {
		cs.Labels = stringMap(labels)
	}
	return cs
}

func stringMap(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// Ensure Client implements Engine.
var _ Engine = (*Client)(nil)
