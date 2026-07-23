package actuate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WorkloadClient patches Application or Worker spec.scaling.desiredReplicas via
// the declarative resource API on forge-control.
type WorkloadClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// ApplicationClient is an alias kept for callers that only scale Applications.
type ApplicationClient = WorkloadClient

// WorkloadView is the subset of the workload envelope the autoscaler needs.
type WorkloadView struct {
	Kind            string
	ResourceVersion string
	DesiredReplicas int
	HasDesired      bool
	Phase           string
	Progressing     bool
	Raw             map[string]any
}

// ApplicationView is an alias for WorkloadView.
type ApplicationView = WorkloadView

// ErrConflict indicates a stale resourceVersion.
var ErrConflict = fmt.Errorf("workload resource version conflict")

// ErrNotFound indicates the workload is missing.
var ErrNotFound = fmt.Errorf("workload not found")

func (c *WorkloadClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func pluralForKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "application":
		return "applications", nil
	case "worker":
		return "workers", nil
	default:
		return "", fmt.Errorf("unsupported target kind %q", kind)
	}
}

func (c *WorkloadClient) workloadURL(project, environment, kind, name string) (string, error) {
	plural, err := pluralForKind(kind)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(c.BaseURL, "/")
	return fmt.Sprintf(
		"%s/v1/projects/%s/environments/%s/%s/%s",
		base,
		url.PathEscape(project),
		url.PathEscape(environment),
		plural,
		url.PathEscape(name),
	), nil
}

// Get fetches a workload resource envelope (Application or Worker).
func (c *WorkloadClient) Get(ctx context.Context, project, environment, kind, name string) (WorkloadView, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return WorkloadView{}, fmt.Errorf("control URL is not configured")
	}
	endpoint, err := c.workloadURL(project, environment, kind, name)
	if err != nil {
		return WorkloadView{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WorkloadView{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return WorkloadView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return WorkloadView{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return WorkloadView{}, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WorkloadView{}, fmt.Errorf("get %s: status %d: %s", kind, resp.StatusCode, truncate(body))
	}
	return parseWorkload(body)
}

// SetDesiredReplicas patches spec.scaling.desiredReplicas on Application or Worker.
// On 409 Conflict it re-reads and retries with the same operationID (max 3 attempts).
func (c *WorkloadClient) SetDesiredReplicas(
	ctx context.Context,
	project, environment, kind, name string,
	desired int,
	operationID string,
) (WorkloadView, error) {
	var last WorkloadView
	for attempt := 0; attempt < 3; attempt++ {
		view, err := c.Get(ctx, project, environment, kind, name)
		if err != nil {
			return WorkloadView{}, err
		}
		last = view
		if view.HasDesired && view.DesiredReplicas == desired {
			return view, nil
		}
		updated, err := c.patchDesired(ctx, project, environment, kind, name, view.ResourceVersion, desired, operationID)
		if err == nil {
			return updated, nil
		}
		if err != ErrConflict {
			return WorkloadView{}, err
		}
	}
	return last, ErrConflict
}

func (c *WorkloadClient) patchDesired(
	ctx context.Context,
	project, environment, kind, name, resourceVersion string,
	desired int,
	operationID string,
) (WorkloadView, error) {
	endpoint, err := c.workloadURL(project, environment, kind, name)
	if err != nil {
		return WorkloadView{}, err
	}
	payload := map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
		},
		"spec": map[string]any{
			"scaling": map[string]any{
				"desiredReplicas": desired,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return WorkloadView{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(raw))
	if err != nil {
		return WorkloadView{}, err
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if operationID != "" {
		req.Header.Set("Idempotency-Key", operationID)
		req.Header.Set("X-Forge-Operation-Id", operationID)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return WorkloadView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return WorkloadView{}, err
	}
	if resp.StatusCode == http.StatusConflict {
		return WorkloadView{}, ErrConflict
	}
	if resp.StatusCode == http.StatusNotFound {
		return WorkloadView{}, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WorkloadView{}, fmt.Errorf("patch %s: status %d: %s", kind, resp.StatusCode, truncate(body))
	}
	return parseWorkload(body)
}

func parseWorkload(body []byte) (WorkloadView, error) {
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		return WorkloadView{}, err
	}
	view := WorkloadView{Raw: env}
	if kind, ok := env["kind"].(string); ok {
		view.Kind = kind
	}
	if meta, ok := env["metadata"].(map[string]any); ok {
		view.ResourceVersion = asString(meta["resourceVersion"])
	}
	if spec, ok := env["spec"].(map[string]any); ok {
		if scaling, ok := spec["scaling"].(map[string]any); ok {
			if v, ok := asInt(scaling["desiredReplicas"]); ok {
				view.DesiredReplicas = v
				view.HasDesired = true
			}
		}
	}
	if status, ok := env["status"].(map[string]any); ok {
		view.Phase = asString(status["phase"])
		view.Progressing = isProgressingPhase(view.Phase)
		if !view.Progressing {
			if conds, ok := status["conditions"].([]any); ok {
				for _, raw := range conds {
					cond, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					ctype := strings.ToLower(asString(cond["type"]))
					cstatus := strings.ToLower(asString(cond["status"]))
					if (ctype == "progressing" || ctype == "rolling") && (cstatus == "true" || cstatus == "unknown") {
						view.Progressing = true
						break
					}
				}
			}
		}
		if !view.Progressing {
			lifecycle := strings.ToLower(asString(status["status"]))
			if lifecycle == "deploying" || lifecycle == "rolling" {
				view.Progressing = true
			}
		}
	}
	return view, nil
}

func isProgressingPhase(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "progressing", "deploying", "rolling", "updating":
		return true
	default:
		return false
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case json.Number:
		i, err := t.Int64()
		return int(i), err == nil
	case string:
		var n int
		_, err := fmt.Sscanf(t, "%d", &n)
		return n, err == nil
	default:
		return 0, false
	}
}

func truncate(b []byte) string {
	const max = 256
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// Actuator is the subset used by the evaluation loop.
type Actuator interface {
	Get(ctx context.Context, project, environment, kind, name string) (WorkloadView, error)
	SetDesiredReplicas(ctx context.Context, project, environment, kind, name string, desired int, operationID string) (WorkloadView, error)
}
