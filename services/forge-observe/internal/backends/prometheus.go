package backends

import "forge.local/services/forge-observe/internal/config"

// NewPrometheus returns a read-only Prometheus client (health: GET /-/healthy).
func NewPrometheus(baseURL string, opts Options) *HTTPClient {
	return newHTTPClient(config.BackendPrometheus, baseURL, "/-/healthy", opts)
}
