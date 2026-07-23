package backends

import "forge.local/services/forge-observe/internal/config"

// NewTempo returns a read-only Tempo client (health: GET /ready).
func NewTempo(baseURL string, opts Options) *HTTPClient {
	return newHTTPClient(config.BackendTempo, baseURL, "/ready", opts)
}
