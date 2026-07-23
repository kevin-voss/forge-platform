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
)

// ObserveSource queries Prometheus-compatible PromQL via Forge Observe's metrics
// backend URL (instant query API). FORGE_OBSERVE_URL is expected to reach a
// Prometheus-compatible /api/v1/query endpoint (Observe stack or direct Prometheus).
type ObserveSource struct {
	BaseURL    string
	HTTPClient *http.Client
}

// Fetch implements MetricSource for cpu / memory / custom utilization queries.
func (s *ObserveSource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	if strings.TrimSpace(s.BaseURL) == "" {
		return Sample{Source: "observe"}, fmt.Errorf("%w: observe URL empty", ErrNotImplemented)
	}
	query := strings.TrimSpace(metric.Query)
	if query == "" {
		query = defaultUtilizationQuery(metric.Type, target)
	}
	if query == "" {
		return Sample{Source: "observe"}, fmt.Errorf("%w: unsupported metric type %q", ErrNotImplemented, metric.Type)
	}

	endpoint := strings.TrimRight(s.BaseURL, "/") + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Sample{Source: "observe"}, err
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Sample{Source: "observe"}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Sample{Source: "observe"}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return Sample{Source: "observe"}, fmt.Errorf("%w: query endpoint missing", ErrNotImplemented)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Sample{Source: "observe"}, fmt.Errorf("observe query status %d", resp.StatusCode)
	}
	value, err := parsePromInstantValue(body)
	if err != nil {
		return Sample{Source: "observe"}, err
	}
	return Sample{
		Value:      value,
		Target:     TargetAverage(metric),
		ObservedAt: time.Now().UTC(),
		Source:     "observe",
	}, nil
}

func defaultUtilizationQuery(metricType string, target policy.TargetRef) string {
	name := strings.TrimSpace(target.Name)
	if name == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(metricType)) {
	case "cpu":
		return fmt.Sprintf(`avg(forge_workload_cpu_utilization{application=%q})`, name)
	case "memory":
		return fmt.Sprintf(`avg(forge_workload_memory_utilization{application=%q})`, name)
	default:
		return ""
	}
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
