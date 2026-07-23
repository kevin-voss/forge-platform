package backends

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"forge.local/services/forge-observe/internal/config"
)

// StatusOK and StatusDown are values returned by GET /v1/health/backends.
const (
	StatusOK   = "ok"
	StatusDown = "down"
)

// Checker is a read-only telemetry backend health client.
type Checker interface {
	Name() config.BackendName
	Healthy(ctx context.Context) error
}

// Metrics tracks backend reachability for dogfooding (forge_observe_backend_up).
type Metrics struct {
	LokiUp       atomic.Int32
	TempoUp      atomic.Int32
	PrometheusUp atomic.Int32
}

// SetUp records whether a backend is currently reachable (1=up, 0=down).
func (m *Metrics) SetUp(name config.BackendName, up bool) {
	if m == nil {
		return
	}
	var v int32
	if up {
		v = 1
	}
	switch name {
	case config.BackendLoki:
		m.LokiUp.Store(v)
	case config.BackendTempo:
		m.TempoUp.Store(v)
	case config.BackendPrometheus:
		m.PrometheusUp.Store(v)
	}
}

// HTTPClient is a read-only HTTP health client for one backend.
type HTTPClient struct {
	name       config.BackendName
	baseURL    string
	healthPath string
	http       *http.Client
	timeout    time.Duration
	metrics    *Metrics
	logChange  func(name config.BackendName, up bool, err error)
	lastUp     atomic.Int32 // -1 unknown, 0 down, 1 up
}

// Options configures a backend HTTP client.
type Options struct {
	Timeout   time.Duration
	HTTP      *http.Client
	Metrics   *Metrics
	LogChange func(name config.BackendName, up bool, err error)
}

func newHTTPClient(name config.BackendName, baseURL, healthPath string, opts Options) *HTTPClient {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{
			// Per-request context deadline is the source of truth; keep transport
			// Timeout unset so context cancellation wins.
			Timeout: 0,
		}
	}
	c := &HTTPClient{
		name:       name,
		baseURL:    strings.TrimRight(baseURL, "/"),
		healthPath: healthPath,
		http:       httpClient,
		timeout:    timeout,
		metrics:    opts.Metrics,
		logChange:  opts.LogChange,
	}
	c.lastUp.Store(-1)
	return c
}

// Name returns the backend identifier.
func (c *HTTPClient) Name() config.BackendName { return c.name }

// Healthy probes the backend ready/healthy endpoint with a context deadline.
func (c *HTTPClient) Healthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+c.healthPath, nil)
	if err != nil {
		c.record(false, err)
		return fmt.Errorf("%s: build request: %w", c.name, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.record(false, err)
		return fmt.Errorf("%s: %w", c.name, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("%s: unexpected status %d", c.name, resp.StatusCode)
		c.record(false, err)
		return err
	}
	c.record(true, nil)
	return nil
}

func (c *HTTPClient) record(up bool, err error) {
	c.metrics.SetUp(c.name, up)
	var next int32
	if up {
		next = 1
	}
	prev := c.lastUp.Swap(next)
	if prev == next {
		return
	}
	if c.logChange != nil {
		c.logChange(c.name, up, err)
	}
}

// Registry holds the three foundation backend clients.
type Registry struct {
	Loki       Checker
	Tempo      Checker
	Prometheus Checker
	Required   []config.BackendName
}

// StatusMap probes every backend and returns ok/down for each.
func (r *Registry) StatusMap(ctx context.Context) map[string]string {
	out := map[string]string{
		string(config.BackendLoki):       StatusDown,
		string(config.BackendTempo):      StatusDown,
		string(config.BackendPrometheus): StatusDown,
	}
	for _, c := range r.all() {
		if c == nil {
			continue
		}
		if err := c.Healthy(ctx); err == nil {
			out[string(c.Name())] = StatusOK
		}
	}
	return out
}

// ReadyError returns nil when every required backend is reachable.
func (r *Registry) ReadyError() error {
	ctx := context.Background()
	var missing []string
	for _, name := range r.Required {
		c := r.byName(name)
		if c == nil {
			missing = append(missing, string(name)+"(unconfigured)")
			continue
		}
		if err := c.Healthy(ctx); err != nil {
			missing = append(missing, string(name))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("backends not ready: %s", strings.Join(missing, ","))
}

func (r *Registry) byName(name config.BackendName) Checker {
	switch name {
	case config.BackendLoki:
		return r.Loki
	case config.BackendTempo:
		return r.Tempo
	case config.BackendPrometheus:
		return r.Prometheus
	default:
		return nil
	}
}

func (r *Registry) all() []Checker {
	return []Checker{r.Loki, r.Tempo, r.Prometheus}
}
