package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"forge.local/services/forge-gateway/internal/proxy"
	"forge.local/services/forge-gateway/internal/routes"
)

const DefaultHostPattern = "{service}.{project}.demo.localhost"

// Result is the outcome of one sync attempt.
type Result struct {
	OK           bool     `json:"ok"`
	RoutesLoaded int      `json:"routesLoaded"`
	Added        []string `json:"added,omitempty"`
	Removed      []string `json:"removed,omitempty"`
	Source       string   `json:"source,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// Syncer periodically fetches endpoints and atomically replaces the route table.
// On source failure the last-good table is retained.
type Syncer struct {
	table    *routes.Table
	proxy    *proxy.Handler
	source   Source
	pattern  string
	interval time.Duration
	log      *slog.Logger
}

// Config wires a Syncer.
type Config struct {
	Table    *routes.Table
	Proxy    *proxy.Handler
	Source   Source
	Pattern  string
	Interval time.Duration
	Log      *slog.Logger
}

// New returns a Syncer. Interval <= 0 disables the background loop (refresh still works).
func New(cfg Config) *Syncer {
	pattern := strings.TrimSpace(cfg.Pattern)
	if pattern == "" {
		pattern = DefaultHostPattern
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &Syncer{
		table:    cfg.Table,
		proxy:    cfg.Proxy,
		source:   cfg.Source,
		pattern:  pattern,
		interval: cfg.Interval,
		log:      log,
	}
}

// Run loops until ctx is cancelled. No-op when interval <= 0 or source is nil.
func (s *Syncer) Run(ctx context.Context) {
	if s == nil || s.source == nil || s.interval <= 0 {
		return
	}
	s.log.Info("route sync loop started",
		"interval_seconds", int(s.interval.Seconds()),
		"source", s.source.Name(),
		"host_pattern", s.pattern,
	)
	// Immediate first sync so routes exist before the first tick.
	s.logResult(s.SyncOnce(ctx))

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("route sync loop stopped")
			return
		case <-ticker.C:
			s.logResult(s.SyncOnce(ctx))
		}
	}
}

// SyncOnce fetches endpoints and replaces the route table on success.
// On failure the existing (last-good) table is kept.
func (s *Syncer) SyncOnce(ctx context.Context) Result {
	if s == nil || s.source == nil {
		return Result{OK: false, Error: "route sync source not configured", RoutesLoaded: s.tableLen()}
	}
	before := s.table.Snapshot()
	endpoints, err := s.source.Fetch(ctx)
	if err != nil {
		s.log.Warn("route sync failed; retaining last-good route table",
			"source", s.source.Name(),
			"error", err.Error(),
			"routes_loaded", len(before),
		)
		return Result{
			OK:           false,
			RoutesLoaded: len(before),
			Source:       s.source.Name(),
			Error:        err.Error(),
		}
	}

	derived := DeriveRoutes(endpoints, s.pattern)
	if err := s.table.Replace(derived); err != nil {
		s.log.Warn("route sync replace failed; retaining last-good route table",
			"source", s.source.Name(),
			"error", err.Error(),
			"routes_loaded", len(before),
		)
		return Result{
			OK:           false,
			RoutesLoaded: len(before),
			Source:       s.source.Name(),
			Error:        err.Error(),
		}
	}
	if s.proxy != nil {
		s.proxy.InvalidatePickers()
	}

	added, removed := DiffHosts(before, derived)
	s.log.Info("route sync complete",
		"source", s.source.Name(),
		"endpoints", len(endpoints),
		"routes_loaded", len(derived),
		"added", added,
		"removed", removed,
	)
	return Result{
		OK:           true,
		RoutesLoaded: len(derived),
		Added:        added,
		Removed:      removed,
		Source:       s.source.Name(),
	}
}

// HandleRefresh serves POST /admin/routes/refresh.
func (s *Syncer) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result := s.SyncOnce(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

// Register mounts the refresh endpoint.
func (s *Syncer) Register(mux *http.ServeMux) {
	if s == nil {
		return
	}
	mux.HandleFunc("POST /admin/routes/refresh", s.HandleRefresh)
}

func (s *Syncer) logResult(result Result) {
	if result.OK {
		return
	}
	// Already logged inside SyncOnce; keep quiet for tick noise.
}

func (s *Syncer) tableLen() int {
	if s == nil || s.table == nil {
		return 0
	}
	return s.table.Len()
}

// DeriveRoutes maps endpoints to gateway routes using the host pattern.
// When Endpoint.Host is already set (Control contract), it wins over the pattern.
func DeriveRoutes(endpoints []Endpoint, pattern string) []routes.Route {
	if pattern == "" {
		pattern = DefaultHostPattern
	}
	// Aggregate upstreams by host so multi-replica deployments share one route.
	type agg struct {
		upstreams []routes.Upstream
	}
	byHost := make(map[string]*agg)
	order := make([]string, 0)

	for _, ep := range endpoints {
		host := strings.ToLower(strings.TrimSpace(ep.Host))
		if host == "" {
			host = ApplyHostPattern(pattern, ep.Service, ep.Project)
		}
		if host == "" || len(ep.Upstreams) == 0 {
			continue
		}
		a, ok := byHost[host]
		if !ok {
			a = &agg{}
			byHost[host] = a
			order = append(order, host)
		}
		for _, u := range ep.Upstreams {
			url := strings.TrimSpace(u.URL)
			if url == "" {
				continue
			}
			a.upstreams = append(a.upstreams, routes.Upstream{URL: url})
		}
	}

	out := make([]routes.Route, 0, len(order))
	for _, host := range order {
		a := byHost[host]
		if len(a.upstreams) == 0 {
			continue
		}
		out = append(out, routes.Route{
			Host:       host,
			PathPrefix: "/",
			Upstreams:  a.upstreams,
			Strategy:   routes.StrategyRoundRobin,
		})
	}
	return out
}

// ApplyHostPattern substitutes {service} and {project} tokens.
func ApplyHostPattern(pattern, service, project string) string {
	service = strings.TrimSpace(service)
	project = strings.TrimSpace(project)
	if service == "" || project == "" {
		return ""
	}
	host := pattern
	host = strings.ReplaceAll(host, "{service}", service)
	host = strings.ReplaceAll(host, "{project}", project)
	return strings.ToLower(strings.TrimSpace(host))
}

// DiffHosts returns sorted hostnames added and removed between snapshots.
func DiffHosts(before, after []routes.Route) (added, removed []string) {
	prev := hostSet(before)
	next := hostSet(after)
	for h := range next {
		if !prev[h] {
			added = append(added, h)
		}
	}
	for h := range prev {
		if !next[h] {
			removed = append(removed, h)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func hostSet(rs []routes.Route) map[string]bool {
	out := make(map[string]bool, len(rs))
	for _, r := range rs {
		h := strings.ToLower(strings.TrimSpace(r.Host))
		if h != "" {
			out[h] = true
		}
	}
	return out
}

// BuildSource constructs the configured endpoint Source.
// control → Control /v1/endpoints with Runtime interim fallback on 404/405.
// runtime → Runtime /v1/node/state + Control metadata join.
func BuildSource(routeSource, controlURL, runtimeURL, upstreamHost string, client HTTPDoer) (Source, error) {
	routeSource = strings.ToLower(strings.TrimSpace(routeSource))
	if routeSource == "" {
		routeSource = "control"
	}
	controlURL = strings.TrimSpace(controlURL)
	runtimeURL = strings.TrimSpace(runtimeURL)
	upstreamHost = strings.TrimSpace(upstreamHost)
	if upstreamHost == "" {
		upstreamHost = "127.0.0.1"
	}

	interim := func() *RuntimeInterimSource {
		return &RuntimeInterimSource{
			ControlURL:   controlURL,
			RuntimeURL:   runtimeURL,
			UpstreamHost: upstreamHost,
			Client:       client,
		}
	}

	switch routeSource {
	case "control":
		if controlURL == "" {
			return nil, fmt.Errorf("FORGE_CONTROL_URL is required when FORGE_ROUTE_SOURCE=control")
		}
		primary := &ControlEndpointsSource{BaseURL: controlURL, Client: client}
		if runtimeURL == "" {
			return primary, nil
		}
		return &FallbackSource{Primary: primary, Interim: interim()}, nil
	case "runtime":
		if runtimeURL == "" {
			return nil, fmt.Errorf("FORGE_RUNTIME_URL is required when FORGE_ROUTE_SOURCE=runtime")
		}
		if controlURL == "" {
			return nil, fmt.Errorf("FORGE_CONTROL_URL is required when FORGE_ROUTE_SOURCE=runtime (hostname metadata)")
		}
		return interim(), nil
	default:
		return nil, fmt.Errorf("FORGE_ROUTE_SOURCE must be control|runtime, got %q", routeSource)
	}
}
