package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

// GatewaySource queries Forge Gateway admin traffic metrics for a target application.
// Expected endpoint: GET {BaseURL}/admin/metrics?application={name}
//
// Response shape (additive consumer; Gateway may expose a subset of fields):
//
//	{
//	  "application": "invoice-api",
//	  "requestsPerSecond": 320.5,
//	  "activeConnections": 42,
//	  "sampleCount": 1500,
//	  "p95LatencySeconds": 0.120,
//	  "errorRate": 0.01
//	}
type GatewaySource struct {
	BaseURL    string
	HTTPClient *http.Client
	Metrics    *telemetry.Registry
}

type gatewayMetricsResponse struct {
	Application       string   `json:"application"`
	RequestsPerSecond *float64 `json:"requestsPerSecond"`
	ActiveConnections *float64 `json:"activeConnections"`
	SampleCount       *int64   `json:"sampleCount"`
	P95LatencySeconds *float64 `json:"p95LatencySeconds"`
	ErrorRate         *float64 `json:"errorRate"`
}

// Fetch implements MetricSource for httpRequests / activeConnections (and optional latency/error fields).
func (s *GatewaySource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	start := time.Now()
	defer func() {
		if s.Metrics != nil {
			s.Metrics.ObserveSourceLatency("gateway", time.Since(start).Seconds())
		}
	}()

	if strings.TrimSpace(s.BaseURL) == "" {
		return Sample{Source: "gateway"}, fmt.Errorf("%w: gateway admin URL empty", ErrNotImplemented)
	}
	name := strings.TrimSpace(target.Name)
	if name == "" {
		return Sample{Source: "gateway"}, fmt.Errorf("%w: target name empty", ErrNotImplemented)
	}

	endpoint := strings.TrimRight(s.BaseURL, "/") + "/admin/metrics?application=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Sample{Source: "gateway"}, err
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Sample{Source: "gateway"}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Sample{Source: "gateway"}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return Sample{Source: "gateway"}, fmt.Errorf("%w: gateway metrics missing for route/application %q", ErrUnavailable, name)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Sample{Source: "gateway"}, fmt.Errorf("gateway metrics status %d", resp.StatusCode)
	}

	var parsed gatewayMetricsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Sample{Source: "gateway"}, err
	}

	metricType := NormalizeMetricType(metric.Type)
	var value *float64
	switch metricType {
	case "httpRequests":
		value = parsed.RequestsPerSecond
	case "activeConnections":
		value = parsed.ActiveConnections
	case "p95Latency":
		value = parsed.P95LatencySeconds
	case "errorRate":
		value = parsed.ErrorRate
	default:
		return Sample{Source: "gateway"}, fmt.Errorf("%w: unsupported gateway metric type %q", ErrNotImplemented, metric.Type)
	}
	if value == nil {
		return Sample{Source: "gateway"}, fmt.Errorf("%w: gateway metric %q unavailable for %q", ErrUnavailable, metricType, name)
	}

	sampleCount := int64(0)
	if parsed.SampleCount != nil {
		sampleCount = *parsed.SampleCount
	}
	return Sample{
		Value:       *value,
		Target:      TargetAverage(metric),
		ObservedAt:  time.Now().UTC(),
		Source:      "gateway",
		SampleCount: sampleCount,
	}, nil
}
