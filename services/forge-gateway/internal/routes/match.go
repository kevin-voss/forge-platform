package routes

import (
	"net"
	"strings"
)

// Match finds the best route for host+path.
// Precedence: host-specific matches beat host-wildcard matches; within a tier,
// the longest matching pathPrefix wins. Ties prefer the earlier route index.
func Match(routes []Route, host, path string) (Route, bool) {
	host = normalizeHost(host)
	if path == "" {
		path = "/"
	}

	bestIdx := -1
	bestHostSpecific := false
	bestPrefixLen := -1

	for i, r := range routes {
		routeHost := strings.ToLower(strings.TrimSpace(r.Host))
		hostSpecific := routeHost != ""
		if hostSpecific && routeHost != host {
			continue
		}
		prefix := r.PathPrefix
		if !pathMatchesPrefix(path, prefix) {
			continue
		}
		prefixLen := len(prefix)

		if bestIdx < 0 {
			bestIdx = i
			bestHostSpecific = hostSpecific
			bestPrefixLen = prefixLen
			continue
		}

		// Host-specific routes win over host-wildcard routes.
		if hostSpecific && !bestHostSpecific {
			bestIdx = i
			bestHostSpecific = true
			bestPrefixLen = prefixLen
			continue
		}
		if !hostSpecific && bestHostSpecific {
			continue
		}

		// Longest path prefix wins; earlier index breaks ties.
		if prefixLen > bestPrefixLen {
			bestIdx = i
			bestPrefixLen = prefixLen
		}
	}

	if bestIdx < 0 {
		return Route{}, false
	}
	return routes[bestIdx], true
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	// Strip optional port.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

func pathMatchesPrefix(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	// Require a path boundary so "/api" does not match "/apiv2".
	if strings.HasSuffix(prefix, "/") {
		return true
	}
	rest := path[len(prefix):]
	return strings.HasPrefix(rest, "/")
}
