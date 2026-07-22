package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Endpoint is a normalized platform endpoint used to derive gateway routes.
// Matches the documented Control GET /v1/endpoints contract.
type Endpoint struct {
	Host      string        `json:"host"`
	Service   string        `json:"service"`
	Project   string        `json:"project"`
	Upstreams []UpstreamRef `json:"upstreams"`
	Ready     bool          `json:"ready"`
}

// UpstreamRef is a single upstream target URL.
type UpstreamRef struct {
	URL string `json:"url"`
}

// Source fetches active endpoints from Control and/or Runtime.
type Source interface {
	Fetch(ctx context.Context) ([]Endpoint, error)
	Name() string
}

// HTTPDoer is the subset of *http.Client used by endpoint clients.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ControlEndpointsSource reads GET {control}/v1/endpoints.
type ControlEndpointsSource struct {
	BaseURL string
	Client  HTTPDoer
}

func (s *ControlEndpointsSource) Name() string { return "control" }

func (s *ControlEndpointsSource) Fetch(ctx context.Context) ([]Endpoint, error) {
	url := strings.TrimRight(s.BaseURL, "/") + "/v1/endpoints"
	var endpoints []Endpoint
	if err := getJSON(ctx, s.client(), url, &endpoints); err != nil {
		return nil, err
	}
	if endpoints == nil {
		endpoints = []Endpoint{}
	}
	return sanitizeEndpoints(endpoints), nil
}

func (s *ControlEndpointsSource) client() HTTPDoer {
	if s.Client != nil {
		return s.Client
	}
	return defaultHTTPClient
}

// RuntimeInterimSource builds endpoints from Runtime GET /v1/node/state
// joined with Control project/service metadata for hostnames.
// Used when Control has no /v1/endpoints read model yet.
type RuntimeInterimSource struct {
	ControlURL   string
	RuntimeURL   string
	UpstreamHost string
	Client       HTTPDoer
}

func (s *RuntimeInterimSource) Name() string { return "runtime" }

func (s *RuntimeInterimSource) Fetch(ctx context.Context) ([]Endpoint, error) {
	state, err := s.fetchNodeState(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := s.fetchDeploymentMeta(ctx)
	if err != nil {
		return nil, err
	}

	host := strings.TrimSpace(s.UpstreamHost)
	if host == "" {
		host = "127.0.0.1"
	}

	out := make([]Endpoint, 0, len(state.Workloads))
	for _, w := range state.Workloads {
		if w.HostPort < 1 || w.HostPort > 65535 {
			continue
		}
		m, ok := meta[w.DeploymentID]
		if !ok {
			// Skip workloads we cannot name; malformed/orphan entries are ignored.
			continue
		}
		ready := strings.EqualFold(w.Status, "ready") || strings.EqualFold(w.Status, "running")
		out = append(out, Endpoint{
			Host:    "", // filled by host pattern during DeriveRoutes
			Service: m.Service,
			Project: m.Project,
			Upstreams: []UpstreamRef{
				{URL: fmt.Sprintf("http://%s:%d", host, w.HostPort)},
			},
			Ready: ready,
		})
	}
	return out, nil
}

type nodeStateResponse struct {
	NodeID    string              `json:"nodeId"`
	Workloads []nodeWorkloadState `json:"workloads"`
}

type nodeWorkloadState struct {
	DeploymentID string `json:"deploymentId"`
	Status       string `json:"status"`
	HostPort     int    `json:"hostPort"`
	Image        string `json:"image,omitempty"`
}

type deploymentMeta struct {
	Service string
	Project string
}

type projectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type projectTree struct {
	Project      projectRef            `json:"project"`
	Applications []applicationTreeNode `json:"applications"`
}

type applicationTreeNode struct {
	Services []serviceTreeNode `json:"services"`
}

type serviceTreeNode struct {
	Name        string               `json:"name"`
	Deployments []deploymentTreeNode `json:"deployments"`
}

type deploymentTreeNode struct {
	ID string `json:"id"`
}

func (s *RuntimeInterimSource) fetchNodeState(ctx context.Context) (nodeStateResponse, error) {
	url := strings.TrimRight(s.RuntimeURL, "/") + "/v1/node/state"
	var state nodeStateResponse
	if err := getJSON(ctx, s.client(), url, &state); err != nil {
		return nodeStateResponse{}, err
	}
	return state, nil
}

func (s *RuntimeInterimSource) fetchDeploymentMeta(ctx context.Context) (map[string]deploymentMeta, error) {
	projectsURL := strings.TrimRight(s.ControlURL, "/") + "/v1/projects"
	var projects []projectRef
	if err := getJSON(ctx, s.client(), projectsURL, &projects); err != nil {
		return nil, err
	}
	out := make(map[string]deploymentMeta)
	for _, p := range projects {
		treeURL := fmt.Sprintf("%s/v1/projects/%s?expand=tree", strings.TrimRight(s.ControlURL, "/"), p.ID)
		var tree projectTree
		if err := getJSON(ctx, s.client(), treeURL, &tree); err != nil {
			return nil, fmt.Errorf("project tree %s: %w", p.ID, err)
		}
		projectKey := strings.TrimSpace(tree.Project.Slug)
		if projectKey == "" {
			projectKey = strings.TrimSpace(tree.Project.Name)
		}
		if projectKey == "" {
			projectKey = strings.TrimSpace(p.Slug)
		}
		if projectKey == "" {
			projectKey = strings.TrimSpace(p.Name)
		}
		for _, app := range tree.Applications {
			for _, svc := range app.Services {
				serviceName := strings.TrimSpace(svc.Name)
				if serviceName == "" || projectKey == "" {
					continue
				}
				for _, dep := range svc.Deployments {
					id := strings.TrimSpace(dep.ID)
					if id == "" {
						continue
					}
					out[id] = deploymentMeta{Service: serviceName, Project: projectKey}
				}
			}
		}
	}
	return out, nil
}

func (s *RuntimeInterimSource) client() HTTPDoer {
	if s.Client != nil {
		return s.Client
	}
	return defaultHTTPClient
}

// FallbackSource tries primary first; on HTTP 404/405 uses interim.
type FallbackSource struct {
	Primary Source
	Interim Source
}

func (s *FallbackSource) Name() string {
	if s.Primary != nil {
		return s.Primary.Name() + "+fallback"
	}
	return "fallback"
}

func (s *FallbackSource) Fetch(ctx context.Context) ([]Endpoint, error) {
	if s.Primary == nil {
		return s.Interim.Fetch(ctx)
	}
	eps, err := s.Primary.Fetch(ctx)
	if err == nil {
		return eps, nil
	}
	if isMissingEndpoint(err) && s.Interim != nil {
		return s.Interim.Fetch(ctx)
	}
	return nil, err
}

type httpStatusError struct {
	Status int
	Body   string
	URL    string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d: %s", e.URL, e.Status, truncate(e.Body, 200))
}

func isMissingEndpoint(err error) bool {
	se, ok := err.(*httpStatusError)
	if !ok {
		return false
	}
	return se.Status == http.StatusNotFound || se.Status == http.StatusMethodNotAllowed
}

var defaultHTTPClient HTTPDoer = &http.Client{Timeout: 10 * time.Second}

func getJSON(ctx context.Context, client HTTPDoer, url string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("GET %s: read body: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{Status: resp.StatusCode, Body: string(body), URL: url}
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("GET %s: decode: %w; body=%s", url, err, truncate(string(body), 200))
	}
	return nil
}

func sanitizeEndpoints(in []Endpoint) []Endpoint {
	out := make([]Endpoint, 0, len(in))
	for _, ep := range in {
		ups := make([]UpstreamRef, 0, len(ep.Upstreams))
		for _, u := range ep.Upstreams {
			url := strings.TrimSpace(u.URL)
			if url == "" {
				continue
			}
			ups = append(ups, UpstreamRef{URL: url})
		}
		if len(ups) == 0 {
			continue
		}
		out = append(out, Endpoint{
			Host:      strings.TrimSpace(ep.Host),
			Service:   strings.TrimSpace(ep.Service),
			Project:   strings.TrimSpace(ep.Project),
			Upstreams: ups,
			Ready:     ep.Ready,
		})
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
