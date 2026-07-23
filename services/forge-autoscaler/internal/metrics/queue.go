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

// QueueSource queries Forge Events / NATS stream metadata for worker queue signals.
// Expected endpoint: GET {BaseURL}/admin/metrics?queue={name}
//
// Response shape (additive consumer; Events may expose a subset of fields):
//
//	{
//	  "queue": "invoice-jobs",
//	  "depth": 20000,
//	  "oldestAgeSeconds": 45.2,
//	  "consumerLag": 1500,
//	  "retryRate": 0.02,
//	  "processingDurationSeconds": 1.5,
//	  "deadLetterCount": 3
//	}
//
// Local mode uses Forge Events stream metadata; the same MetricSource seam later
// points at Forge Queue (epic 28) without autoscaler-side changes.
type QueueSource struct {
	BaseURL    string
	HTTPClient *http.Client
	Metrics    *telemetry.Registry
}

type queueMetricsResponse struct {
	Queue                     string   `json:"queue"`
	Depth                     *float64 `json:"depth"`
	OldestAgeSeconds          *float64 `json:"oldestAgeSeconds"`
	ConsumerLag               *float64 `json:"consumerLag"`
	RetryRate                 *float64 `json:"retryRate"`
	ProcessingDurationSeconds *float64 `json:"processingDurationSeconds"`
	DeadLetterCount           *float64 `json:"deadLetterCount"`
}

// Fetch implements MetricSource for queueDepth and related worker signals.
func (s *QueueSource) Fetch(ctx context.Context, target policy.TargetRef, metric policy.MetricSpec) (Sample, error) {
	start := time.Now()
	defer func() {
		if s.Metrics != nil {
			s.Metrics.ObserveSourceLatency("queue", time.Since(start).Seconds())
		}
	}()

	if strings.TrimSpace(s.BaseURL) == "" {
		return Sample{Source: "queue"}, fmt.Errorf("%w: events/queue URL empty", ErrNotImplemented)
	}
	queue := QueueName(target, metric)
	if queue == "" {
		return Sample{Source: "queue"}, fmt.Errorf("%w: queue name empty", ErrNotImplemented)
	}

	endpoint := strings.TrimRight(s.BaseURL, "/") + "/admin/metrics?queue=" + url.QueryEscape(queue)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Sample{Source: "queue"}, err
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Sample{Source: "queue"}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Sample{Source: "queue"}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return Sample{Source: "queue"}, fmt.Errorf("%w: queue metrics missing for %q", ErrUnavailable, queue)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Sample{Source: "queue"}, fmt.Errorf("queue metrics status %d", resp.StatusCode)
	}

	var parsed queueMetricsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Sample{Source: "queue"}, err
	}

	metricType := NormalizeMetricType(metric.Type)
	var value *float64
	switch metricType {
	case "queueDepth":
		value = parsed.Depth
	case "oldestMessageAge":
		value = parsed.OldestAgeSeconds
	case "consumerLag":
		value = parsed.ConsumerLag
	case "retryRate":
		value = parsed.RetryRate
	case "processingDuration":
		value = parsed.ProcessingDurationSeconds
	case "deadLetterPressure":
		value = parsed.DeadLetterCount
	default:
		return Sample{Source: "queue"}, fmt.Errorf("%w: unsupported queue metric type %q", ErrNotImplemented, metric.Type)
	}
	if value == nil {
		return Sample{Source: "queue"}, fmt.Errorf("%w: queue metric %q unavailable for %q", ErrUnavailable, metricType, queue)
	}

	if s.Metrics != nil && metricType == "queueDepth" {
		s.Metrics.SetQueueBacklog(queue, *value)
	}

	return Sample{
		Value:      *value,
		Target:     TargetAverage(metric),
		ObservedAt: time.Now().UTC(),
		Source:     "queue",
		QueueName:  queue,
	}, nil
}

// QueueName resolves the queue identifier from MetricSpec or target name.
func QueueName(target policy.TargetRef, metric policy.MetricSpec) string {
	if q := strings.TrimSpace(metric.Queue); q != "" {
		return q
	}
	if q := strings.TrimSpace(metric.Query); q != "" {
		return q
	}
	return strings.TrimSpace(target.Name)
}
