package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/policy"
	"forge.local/services/forge-autoscaler/internal/telemetry"
)

// ObserveSource queries Prometheus-compatible PromQL via Forge Observe's metrics
// backend URL (instant query API). FORGE_OBSERVE_URL is expected to reach a
// Prometheus-compatible /api/v1/query endpoint (Observe stack or direct Prometheus).
type ObserveSource struct {
	BaseURL    string
	HTTPClient *http.Client
	Metrics    *telemetry.Registry
}

// Fetch implements MetricSource for cpu / memory / traffic / custom queries.
func (s *ObserveSource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	start := time.Now()
	defer func() {
		if s.Metrics != nil {
			s.Metrics.ObserveSourceLatency("observe", time.Since(start).Seconds())
		}
	}()

	if strings.TrimSpace(s.BaseURL) == "" {
		return Sample{Source: "observe"}, fmt.Errorf("%w: observe URL empty", ErrNotImplemented)
	}
	query := strings.TrimSpace(metric.Query)
	if query == "" {
		query = defaultObserveQuery(metric.Type, target)
	}
	if query == "" {
		return Sample{Source: "observe"}, fmt.Errorf("%w: unsupported metric type %q", ErrNotImplemented, metric.Type)
	}

	value, err := s.queryInstant(ctx, query)
	if err != nil {
		return Sample{Source: "observe"}, err
	}

	sampleCount := int64(0)
	if IsGuardrailMetric(metric.Type) {
		countQuery := defaultSampleCountQuery(target)
		if countQuery != "" {
			if count, cerr := s.queryInstant(ctx, countQuery); cerr == nil {
				sampleCount = int64(count)
			}
		}
	}

	return Sample{
		Value:       value,
		Target:      TargetAverage(metric),
		ObservedAt:  time.Now().UTC(),
		Source:      "observe",
		SampleCount: sampleCount,
	}, nil
}

func (s *ObserveSource) queryInstant(ctx context.Context, query string) (float64, error) {
	endpoint := strings.TrimRight(s.BaseURL, "/") + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("%w: query endpoint missing", ErrNotImplemented)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("observe query status %d", resp.StatusCode)
	}
	return parsePromInstantValue(body)
}

func defaultObserveQuery(metricType string, target policy.TargetRef) string {
	name := strings.TrimSpace(target.Name)
	if name == "" {
		return ""
	}
	switch NormalizeMetricType(metricType) {
	case "cpu":
		return fmt.Sprintf(`avg(forge_workload_cpu_utilization{application=%q})`, name)
	case "memory":
		return fmt.Sprintf(`avg(forge_workload_memory_utilization{application=%q})`, name)
	case "httpRequests":
		return fmt.Sprintf(`sum(rate(forge_http_requests_total{application=%q}[1m]))`, name)
	case "activeConnections":
		return fmt.Sprintf(`sum(forge_gateway_active_connections{application=%q})`, name)
	case "p95Latency":
		return fmt.Sprintf(
			`histogram_quantile(0.95, sum(rate(forge_http_request_duration_seconds_bucket{application=%q}[5m])) by (le))`,
			name,
		)
	case "errorRate":
		return fmt.Sprintf(
			`sum(rate(forge_http_requests_total{application=%q,http_status_class="5xx"}[5m])) / clamp_min(sum(rate(forge_http_requests_total{application=%q}[5m])), 1e-9)`,
			name, name,
		)
	default:
		return ""
	}
}

func defaultSampleCountQuery(target policy.TargetRef) string {
	name := strings.TrimSpace(target.Name)
	if name == "" {
		return ""
	}
	// Approximate sample volume over the latency window.
	return fmt.Sprintf(`sum(increase(forge_http_requests_total{application=%q}[5m]))`, name)
}

type promQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func parsePromInstantValue(body []byte) (float64, error) {
	var parsed promQueryResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if parsed.Status != "success" {
		return 0, fmt.Errorf("prometheus query status %q", parsed.Status)
	}
	if len(parsed.Data.Result) == 0 {
		return 0, fmt.Errorf("prometheus query returned no series")
	}
	value := parsed.Data.Result[0].Value
	if len(value) < 2 {
		return 0, fmt.Errorf("prometheus value malformed")
	}
	switch v := value[1].(type) {
	case string:
		return strconv.ParseFloat(v, 64)
	case float64:
		return v, nil
	default:
		return 0, fmt.Errorf("prometheus value type %T unsupported", v)
	}
}
