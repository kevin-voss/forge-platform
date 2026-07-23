package sync

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// DiscoveryEndpointsSource fetches Ready endpoints from Forge Discovery and
// expands Service.spec.aliases into additional gateway hostnames.
type DiscoveryEndpointsSource struct {
	BaseURL     string
	HostPattern string
	Client      HTTPDoer
	Log         *slog.Logger
}

func (s *DiscoveryEndpointsSource) Name() string { return "discovery" }

type discoveryService struct {
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
}

type discoveryEndpointItem struct {
	ID      string `json:"id"`
	Service string `json:"service"`
	Phase   string `json:"phase"`
	Ready   bool   `json:"ready"`
	Address struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"address"`
}

func (s *DiscoveryEndpointsSource) Fetch(ctx context.Context) ([]Endpoint, error) {
	base := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("discovery base URL is empty")
	}
	pattern := strings.TrimSpace(s.HostPattern)
	if pattern == "" {
		pattern = DefaultHostPattern
	}

	var services []discoveryService
	if err := getJSON(ctx, s.client(), base+"/v1/services", &services); err != nil {
		return nil, err
	}
	if services == nil {
		services = []discoveryService{}
	}

	out := make([]Endpoint, 0)
	for _, svc := range services {
		project := strings.TrimSpace(svc.Project)
		env := strings.TrimSpace(svc.Environment)
		name := strings.TrimSpace(svc.Name)
		if project == "" || env == "" || name == "" {
			continue
		}
		eps, err := s.fetchServiceEndpoints(ctx, base, project, env, name)
		if err != nil {
			s.log().Warn("discovery endpoint list failed; omitting service for this sync cycle",
				"project", project,
				"environment", env,
				"service", name,
				"error", err.Error(),
			)
			continue
		}
		canonical, upstreams := mapDiscoveryEndpoints(name, project, eps)
		out = append(out, canonical...)
		out = append(out, expandAliasEndpoints(name, project, svc.Aliases, upstreams, pattern, s.log())...)
	}
	return out, nil
}

func (s *DiscoveryEndpointsSource) fetchServiceEndpoints(ctx context.Context, base, project, env, service string) ([]discoveryEndpointItem, error) {
	path := fmt.Sprintf("%s/v1/projects/%s/environments/%s/services/%s/endpoints",
		base,
		url.PathEscape(project),
		url.PathEscape(env),
		url.PathEscape(service),
	)
	var items []discoveryEndpointItem
	if err := getJSON(ctx, s.client(), path, &items); err != nil {
		return nil, err
	}
	if items == nil {
		items = []discoveryEndpointItem{}
	}
	return items, nil
}

func mapDiscoveryEndpoints(service, project string, items []discoveryEndpointItem) ([]Endpoint, []UpstreamRef) {
	ready := true
	out := make([]Endpoint, 0, len(items))
	upstreams := make([]UpstreamRef, 0, len(items))
	for _, item := range items {
		ip := strings.TrimSpace(item.Address.IP)
		if ip == "" || item.Address.Port < 1 || item.Address.Port > 65535 {
			continue
		}
		u := UpstreamRef{URL: fmt.Sprintf("http://%s:%d", ip, item.Address.Port)}
		upstreams = append(upstreams, u)
		out = append(out, Endpoint{
			Host:      "", // filled by host pattern during DeriveRoutes
			Service:   service,
			Project:   project,
			Upstreams: []UpstreamRef{u},
			Ready:     &ready,
		})
	}
	return out, upstreams
}

func expandAliasEndpoints(canonicalService, project string, aliases []string, upstreams []UpstreamRef, pattern string, log *slog.Logger) []Endpoint {
	if len(upstreams) == 0 || len(aliases) == 0 {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	ready := true
	out := make([]Endpoint, 0, len(aliases))
	seen := map[string]bool{}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || strings.EqualFold(alias, canonicalService) {
			continue
		}
		host := ApplyHostPattern(pattern, alias, project)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		ups := make([]UpstreamRef, len(upstreams))
		copy(ups, upstreams)
		out = append(out, Endpoint{
			Host:      host,
			Service:   canonicalService,
			Project:   project,
			Upstreams: ups,
			Ready:     &ready,
		})
		log.Info("gateway.alias.route_added",
			"event", "gateway.alias.route_added",
			"alias", alias,
			"canonical_service", canonicalService,
			"host", host,
			"project", project,
		)
	}
	return out
}

func (s *DiscoveryEndpointsSource) client() HTTPDoer {
	if s.Client != nil {
		return s.Client
	}
	return defaultHTTPClient
}

func (s *DiscoveryEndpointsSource) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
