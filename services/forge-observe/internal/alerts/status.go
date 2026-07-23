package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrUnavailable is returned when the alerting backend cannot be reached.
var ErrUnavailable = errors.New("alerting backend unavailable")

// Bounded label keys returned on GET /v1/alerts (no secrets/PII).
var allowedLabels = map[string]struct{}{
	"alertname":        {},
	"severity":         {},
	"service":          {},
	"service_name":     {},
	"forge_service":    {},
	"forge_project":    {},
	"project":          {},
	"forge_deployment": {},
	"deployment":       {},
	"forge_node":       {},
	"node":             {},
}

// Alert is the normalized Observe alert status shape.
type Alert struct {
	Name   string            `json:"name"`
	State  string            `json:"state"` // firing | pending
	Labels map[string]string `json:"labels"`
	Since  time.Time         `json:"since"`
	Value  float64           `json:"value"`
}

// StatusClient loads active/pending alerts from Prometheus rule evaluation,
// gated on Alertmanager reachability (local delivery backend).
type StatusClient struct {
	AlertmanagerURL string
	PrometheusURL   string
	HTTP            *http.Client
	Timeout         time.Duration
	Now             func() time.Time
}

type promAlertsResponse struct {
	Status string `json:"status"`
	Data   struct {
		Alerts []promAlert `json:"alerts"`
	} `json:"data"`
}

type promAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    time.Time         `json:"activeAt"`
	Value       string            `json:"value"`
}

// List returns firing and pending alerts with bounded labels.
// Alertmanager must be reachable; otherwise ErrUnavailable is returned (503).
func (c *StatusClient) List(ctx context.Context) ([]Alert, error) {
	if c == nil || strings.TrimSpace(c.AlertmanagerURL) == "" {
		return nil, ErrUnavailable
	}
	if err := c.pingAlertmanager(ctx); err != nil {
		return nil, err
	}
	promURL := strings.TrimRight(strings.TrimSpace(c.PrometheusURL), "/")
	if promURL == "" {
		return nil, ErrUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, promURL+"/api/v1/alerts", nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusNotFound {
		return nil, ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: prometheus alerts status %d", ErrUnavailable, resp.StatusCode)
	}

	var parsed promAlertsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode prometheus alerts: %w", err)
	}
	out := make([]Alert, 0, len(parsed.Data.Alerts))
	for _, a := range parsed.Data.Alerts {
		norm, ok := normalizePromAlert(a)
		if !ok {
			continue
		}
		out = append(out, norm)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out, nil
}

func (c *StatusClient) pingAlertmanager(ctx context.Context) error {
	base := strings.TrimRight(strings.TrimSpace(c.AlertmanagerURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/-/healthy", nil)
	if err != nil {
		return ErrUnavailable
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	return nil
}

func (c *StatusClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func normalizePromAlert(a promAlert) (Alert, bool) {
	state := strings.ToLower(strings.TrimSpace(a.State))
	switch state {
	case "firing", "pending":
	default:
		return Alert{}, false
	}
	name := strings.TrimSpace(a.Labels["alertname"])
	if name == "" {
		return Alert{}, false
	}
	labels := filterLabels(a.Labels)
	since := a.ActiveAt.UTC()
	if since.IsZero() {
		since = time.Now().UTC()
	}
	value, _ := strconv.ParseFloat(strings.TrimSpace(a.Value), 64)
	return Alert{
		Name:   name,
		State:  state,
		Labels: labels,
		Since:  since,
		Value:  value,
	}, true
}

func filterLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if _, ok := allowedLabels[key]; !ok {
			continue
		}
		val := strings.TrimSpace(v)
		if val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

// ProjectLabel returns the project id from bounded alert labels, if any.
func ProjectLabel(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if p := strings.TrimSpace(labels["forge_project"]); p != "" {
		return p
	}
	return strings.TrimSpace(labels["project"])
}

// FilterByProjects keeps alerts whose project label is in allowed, plus
// platform-level alerts with no project label. When allowed is nil, all pass.
func FilterByProjects(in []Alert, allowed map[string]struct{}) []Alert {
	if allowed == nil {
		return in
	}
	out := make([]Alert, 0, len(in))
	for _, a := range in {
		p := ProjectLabel(a.Labels)
		if p == "" {
			out = append(out, a)
			continue
		}
		if _, ok := allowed[p]; ok {
			out = append(out, a)
		}
	}
	return out
}

// NormalizeAlertmanagerWebhook is used by tests to assert webhook payload shape.
func NormalizeAlertmanagerWebhook(payload []byte) (status string, names []string, err error) {
	var body struct {
		Status string `json:"status"`
		Alerts []struct {
			Labels map[string]string `json:"labels"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", nil, err
	}
	seen := map[string]struct{}{}
	for _, a := range body.Alerts {
		n := strings.TrimSpace(a.Labels["alertname"])
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	sort.Strings(names)
	return body.Status, names, nil
}
