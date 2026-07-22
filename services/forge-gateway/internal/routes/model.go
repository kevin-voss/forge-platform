package routes

import (
	"fmt"
	"net/url"
	"strings"
)

const StrategyRoundRobin = "round_robin"

// Upstream is a single backend target for a route.
type Upstream struct {
	URL string `json:"url"`
}

// Route is a host and/or path-prefix rule with one or more upstreams.
type Route struct {
	Host       string     `json:"host,omitempty"`
	PathPrefix string     `json:"pathPrefix,omitempty"`
	Upstreams  []Upstream `json:"upstreams"`
	Strategy   string     `json:"strategy"`
}

// Validate checks a route snapshot for use in the route table.
func Validate(routes []Route) error {
	for i, r := range routes {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("route[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate checks a single route.
func (r Route) Validate() error {
	strategy := strings.TrimSpace(r.Strategy)
	if strategy == "" {
		strategy = StrategyRoundRobin
	}
	if strategy != StrategyRoundRobin {
		return fmt.Errorf("unsupported strategy %q (only %q)", strategy, StrategyRoundRobin)
	}
	if len(r.Upstreams) == 0 {
		return fmt.Errorf("at least one upstream is required")
	}
	prefix := r.PathPrefix
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("pathPrefix must be empty or start with /, got %q", prefix)
	}
	for j, u := range r.Upstreams {
		if err := validateUpstreamURL(u.URL); err != nil {
			return fmt.Errorf("upstreams[%d]: %w", j, err)
		}
	}
	return nil
}

// Normalized returns a copy with trimmed fields and default strategy.
func (r Route) Normalized() Route {
	out := Route{
		Host:       strings.ToLower(strings.TrimSpace(r.Host)),
		PathPrefix: strings.TrimSpace(r.PathPrefix),
		Strategy:   strings.TrimSpace(r.Strategy),
		Upstreams:  make([]Upstream, len(r.Upstreams)),
	}
	if out.Strategy == "" {
		out.Strategy = StrategyRoundRobin
	}
	for i, u := range r.Upstreams {
		out.Upstreams[i] = Upstream{URL: strings.TrimSpace(u.URL)}
	}
	return out
}

func validateUpstreamURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url host is required")
	}
	if u.User != nil {
		return fmt.Errorf("url must not include userinfo")
	}
	return nil
}
