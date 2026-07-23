package backends

import "forge.local/services/forge-observe/internal/config"

// NewLoki returns a read-only Loki client (health: GET /ready).
func NewLoki(baseURL string, opts Options) *HTTPClient {
	return newHTTPClient(config.BackendLoki, baseURL, "/ready", opts)
}
