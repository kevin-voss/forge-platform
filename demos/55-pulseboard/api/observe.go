package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ObserveClient queries Forge Observe's Prometheus-compatible metrics backend
// (instant query). FORGE_OBSERVE_URL may point at forge-observe, Prometheus, or
// the demo Observe metrics sidecar that exposes /api/v1/query.
type ObserveClient struct {
	BaseURL    string
	App        string
	HTTPClient *http.Client
}

func newObserveClientFromEnv() *ObserveClient {
	base := strings.TrimSpace(os.Getenv("FORGE_OBSERVE_URL"))
	app := strings.TrimSpace(os.Getenv("PULSEBOARD_APPLICATION"))
	if app == "" {
		app = "pulseboard-api"
	}
	if base == "" {
		return &ObserveClient{App: app}
	}
	return &ObserveClient{
		BaseURL: strings.TrimRight(base, "/"),
		App:     app,
		HTTPClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// PlatformStats is the Observe-sourced slice of the dashboard payload.
type PlatformStats struct {
	Replicas int
	RPS      float64
	P95Ms    float64
	Source   string
}

func (c *ObserveClient) Enabled() bool {
	return c != nil && strings.TrimSpace(c.BaseURL) != ""
}

func (c *ObserveClient) FetchPlatformStats(ctx context.Context) (PlatformStats, error) {
	if !c.Enabled() {
		return PlatformStats{}, fmt.Errorf("observe URL empty")
	}
	app := c.App
	replicasQ := fmt.Sprintf(`sum(forge_replicas_ready{application=%q}) or sum(forge_replicas_ready_total{application=%q})`, app, app)
	rpsQ := fmt.Sprintf(`sum(rate(forge_http_requests_total{application=%q}[1m]))`, app)
	p95Q := fmt.Sprintf(
		`histogram_quantile(0.95, sum(rate(forge_http_request_duration_seconds_bucket{application=%q}[5m])) by (le))`,
		app,
	)

	replicas, err := c.queryInstant(ctx, replicasQ)
	if err != nil {
		return PlatformStats{}, fmt.Errorf("replicas: %w", err)
	}
	rps, err := c.queryInstant(ctx, rpsQ)
	if err != nil {
		return PlatformStats{}, fmt.Errorf("rps: %w", err)
	}
	p95Sec, err := c.queryInstant(ctx, p95Q)
	if err != nil {
		// Fall back to summary quantile series when histogram buckets are absent.
		p95Sec, err = c.queryInstant(ctx, fmt.Sprintf(
			`forge_http_request_duration_seconds{quantile="0.95",application=%q}`, app,
		))
		if err != nil {
			return PlatformStats{}, fmt.Errorf("p95: %w", err)
		}
	}

	return PlatformStats{
		Replicas: int(math.Round(replicas)),
		RPS:      rps,
		P95Ms:    p95Sec * 1000.0,
		Source:   "observe",
	}, nil
}

func (c *ObserveClient) queryInstant(ctx context.Context, query string) (float64, error) {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}

	// Prefer Observe metrics facade; fall back to Prometheus instant-query path.
	paths := []string{"/v1/metrics/query", "/api/v1/query"}
	var lastErr error
	for _, path := range paths {
		endpoint := c.BaseURL + path + "?query=" + url.QueryEscape(query)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			lastErr = fmt.Errorf("query endpoint missing: %s", path)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("observe query status %d", resp.StatusCode)
			continue
		}
		value, parseErr := parseObserveInstantValue(body)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}
		return value, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("observe query failed")
	}
	return 0, lastErr
}

type promQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
	// Observe facade shape (agents metrics.query).
	ResultType string `json:"result_type"`
	Samples    []struct {
		Value any `json:"value"`
	} `json:"samples"`
}

func parseObserveInstantValue(body []byte) (float64, error) {
	var parsed promQueryResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if len(parsed.Samples) > 0 {
		return coerceFloat(parsed.Samples[0].Value)
	}
	if parsed.Status != "" && parsed.Status != "success" {
		return 0, fmt.Errorf("prometheus query status %q", parsed.Status)
	}
	if len(parsed.Data.Result) == 0 {
		return 0, fmt.Errorf("prometheus query returned no series")
	}
	value := parsed.Data.Result[0].Value
	if len(value) < 2 {
		return 0, fmt.Errorf("prometheus value malformed")
	}
	return coerceFloat(value[1])
}

func coerceFloat(v any) (float64, error) {
	switch n := v.(type) {
	case string:
		return strconv.ParseFloat(n, 64)
	case float64:
		return n, nil
	case json.Number:
		return n.Float64()
	default:
		return 0, fmt.Errorf("value type %T unsupported", v)
	}
}
