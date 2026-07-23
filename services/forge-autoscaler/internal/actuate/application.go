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

// ApplicationClient patches Application.spec.scaling.desiredReplicas via the
// declarative resource API on forge-control.
type ApplicationClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// ApplicationView is the subset of the Application envelope the autoscaler needs.
type ApplicationView struct {
	ResourceVersion string
	DesiredReplicas int
	HasDesired      bool
	Raw             map[string]any
}

// ErrConflict indicates a stale resourceVersion.
var ErrConflict = fmt.Errorf("application resource version conflict")

// ErrNotFound indicates the Application is missing.
var ErrNotFound = fmt.Errorf("application not found")

func (c *ApplicationClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *ApplicationClient) appURL(project, environment, name string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	return fmt.Sprintf(
		"%s/v1/projects/%s/environments/%s/applications/%s",
		base,
		url.PathEscape(project),
		url.PathEscape(environment),
		url.PathEscape(name),
	)
}

// Get fetches an Application resource envelope.
func (c *ApplicationClient) Get(ctx context.Context, project, environment, name string) (ApplicationView, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return ApplicationView{}, fmt.Errorf("control URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.appURL(project, environment, name), nil)
	if err != nil {
		return ApplicationView{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return ApplicationView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ApplicationView{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ApplicationView{}, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ApplicationView{}, fmt.Errorf("get application: status %d: %s", resp.StatusCode, truncate(body))
	}
	return parseApplication(body)
}

// SetDesiredReplicas patches Application.spec.scaling.desiredReplicas.
// On 409 Conflict it re-reads and retries with the same operationID (max 3 attempts).
func (c *ApplicationClient) SetDesiredReplicas(
	ctx context.Context,
	project, environment, name string,
	desired int,
	operationID string,
) (ApplicationView, error) {
	var last ApplicationView
	for attempt := 0; attempt < 3; attempt++ {
		view, err := c.Get(ctx, project, environment, name)
		if err != nil {
			return ApplicationView{}, err
		}
		last = view
		if view.HasDesired && view.DesiredReplicas == desired {
			return view, nil
		}
		updated, err := c.patchDesired(ctx, project, environment, name, view.ResourceVersion, desired, operationID)
		if err == nil {
			return updated, nil
		}
		if err != ErrConflict {
			return ApplicationView{}, err
		}
	}
	return last, ErrConflict
}

func (c *ApplicationClient) patchDesired(
	ctx context.Context,
	project, environment, name, resourceVersion string,
	desired int,
	operationID string,
) (ApplicationView, error) {
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
		return ApplicationView{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.appURL(project, environment, name), bytes.NewReader(raw))
	if err != nil {
		return ApplicationView{}, err
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if operationID != "" {
		req.Header.Set("Idempotency-Key", operationID)
		req.Header.Set("X-Forge-Operation-Id", operationID)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return ApplicationView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ApplicationView{}, err
	}
	if resp.StatusCode == http.StatusConflict {
		return ApplicationView{}, ErrConflict
	}
	if resp.StatusCode == http.StatusNotFound {
		return ApplicationView{}, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ApplicationView{}, fmt.Errorf("patch application: status %d: %s", resp.StatusCode, truncate(body))
	}
	return parseApplication(body)
}

func parseApplication(body []byte) (ApplicationView, error) {
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		return ApplicationView{}, err
	}
	view := ApplicationView{Raw: env}
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
	return view, nil
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
	Get(ctx context.Context, project, environment, name string) (ApplicationView, error)
	SetDesiredReplicas(ctx context.Context, project, environment, name string, desired int, operationID string) (ApplicationView, error)
}
