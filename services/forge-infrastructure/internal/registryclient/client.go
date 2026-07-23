package registryclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const controllerName = "forge-infrastructure"

// Resource is a generic epic-20 envelope.
type Resource struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   Metadata       `json:"metadata"`
	Spec       map[string]any `json:"spec,omitempty"`
	Status     map[string]any `json:"status,omitempty"`
}

// Metadata holds resource identity fields.
type Metadata struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name"`
	Generation      int64             `json:"generation,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

// ListResponse is a cluster-scoped list envelope.
type ListResponse struct {
	APIVersion      string     `json:"apiVersion"`
	Kind            string     `json:"kind"`
	ResourceVersion string     `json:"resourceVersion"`
	Items           []Resource `json:"items"`
	NextCursor      string     `json:"nextCursor,omitempty"`
}

// KindRegistration is POST /v1/kinds body.
type KindRegistration struct {
	APIVersion    string `json:"apiVersion"`
	Kind          string `json:"kind"`
	Plural        string `json:"plural"`
	Scope         string `json:"scope"`
	Controller    string `json:"controller"`
	SchemaVersion int    `json:"schemaVersion"`
	IDPrefix      string `json:"idPrefix,omitempty"`
}

// Client talks to epic-20's /v1/{kind-plural} API (hosted by Control).
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Log        *slog.Logger
	Controller string
}

// New returns a registry client.
func New(baseURL string, log *slog.Logger) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		Log:        log,
		Controller: controllerName,
	}
}

// Kind payloads ---------------------------------------------------------------

// InfrastructureProviderKindPayload registers InfrastructureProvider.
func InfrastructureProviderKindPayload() KindRegistration {
	return KindRegistration{
		APIVersion:    "forge.dev/v1",
		Kind:          "InfrastructureProvider",
		Plural:        "infrastructureproviders",
		Scope:         "cluster",
		Controller:    controllerName,
		SchemaVersion: 1,
		IDPrefix:      "infra",
	}
}

// NodePoolKindPayload registers NodePool.
func NodePoolKindPayload() KindRegistration {
	return KindRegistration{
		APIVersion:    "forge.dev/v1",
		Kind:          "NodePool",
		Plural:        "nodepools",
		Scope:         "cluster",
		Controller:    controllerName,
		SchemaVersion: 1,
		IDPrefix:      "pool",
	}
}

// NodePlural is the cluster-scoped resource plural for infrastructure Node.
// Distinct from Control's scheduler fleet GET /v1/nodes (bare JSON array).
const NodePlural = "forgenodes"

// NodeKindPayload registers Node.
func NodeKindPayload() KindRegistration {
	return KindRegistration{
		APIVersion:    "forge.dev/v1",
		Kind:          "Node",
		Plural:        NodePlural,
		Scope:         "cluster",
		Controller:    controllerName,
		SchemaVersion: 1,
		IDPrefix:      "node",
	}
}

// RegisterKinds posts the three infrastructure kinds with retry/backoff.
func (c *Client) RegisterKinds(ctx context.Context, maxWait time.Duration) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("registry URL is required")
	}
	deadline := time.Now().Add(maxWait)
	backoff := 500 * time.Millisecond
	var lastErr error
	for {
		err := c.registerOnce(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if c.Log != nil {
			c.Log.Warn("kind registration attempt failed",
				"event", "infra.kind_registered",
				"error", err.Error(),
			)
		}
		if time.Now().Add(backoff).After(deadline) {
			return fmt.Errorf("kind registration failed after %s: %w", maxWait, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) registerOnce(ctx context.Context) error {
	for _, payload := range []KindRegistration{
		InfrastructureProviderKindPayload(),
		NodePoolKindPayload(),
		NodeKindPayload(),
	} {
		result, err := c.postKind(ctx, payload)
		if err != nil {
			return err
		}
		if c.Log != nil {
			c.Log.Info("kind registered",
				"event", "infra.kind_registered",
				"kind", payload.Kind,
				"plural", payload.Plural,
				"result", result,
			)
		}
	}
	return nil
}

func (c *Client) postKind(ctx context.Context, payload KindRegistration) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/kinds", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /v1/kinds: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		return "already_registered", nil
	case http.StatusCreated:
		return "created", nil
	default:
		return "", fmt.Errorf("POST /v1/kinds %s: status %d body %s", payload.Kind, resp.StatusCode, truncate(string(data), 256))
	}
}

// CRUD -----------------------------------------------------------------------

// Get fetches a cluster-scoped resource by plural + name.
func (c *Client) Get(ctx context.Context, plural, name string) (*Resource, error) {
	var out Resource
	status, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/%s/%s", plural, name), nil, &out, false)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("GET /v1/%s/%s: status %d", plural, name, status)
	}
	return &out, nil
}

// List returns cluster-scoped resources, optionally filtered by labelSelector.
func (c *Client) List(ctx context.Context, plural, labelSelector string) ([]Resource, error) {
	path := "/v1/" + plural
	if labelSelector != "" {
		path += "?labelSelector=" + url.QueryEscape(labelSelector)
	}
	raw, status, err := c.doRaw(ctx, http.MethodGet, path, nil, false)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("GET %s: status %d body %s", path, status, truncate(string(raw), 256))
	}
	// Defensive: a bare JSON array is never a resource list envelope.
	trim := bytes.TrimSpace(raw)
	if len(trim) > 0 && trim[0] == '[' {
		if c.Log != nil {
			c.Log.Warn("list returned non-resource array; treating as empty",
				"plural", plural,
			)
		}
		return nil, nil
	}
	var out ListResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	return out.Items, nil
}

// Create posts a new cluster-scoped resource.
func (c *Client) Create(ctx context.Context, plural string, res Resource) (*Resource, error) {
	if res.APIVersion == "" {
		res.APIVersion = "forge.dev/v1"
	}
	var out Resource
	status, err := c.doJSON(ctx, http.MethodPost, "/v1/"+plural, res, &out, false)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("POST /v1/%s: status %d", plural, status)
	}
	return &out, nil
}

// PutStatus writes the status subresource (requires X-Forge-Controller).
func (c *Client) PutStatus(ctx context.Context, plural, name, resourceVersion string, status map[string]any) (*Resource, error) {
	body := map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
		},
		"status": status,
	}
	var out Resource
	code, err := c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/v1/%s/%s/status", plural, name), body, &out, true)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("PUT /v1/%s/%s/status: status %d", plural, name, code)
	}
	return &out, nil
}

// Delete removes a cluster-scoped resource by plural + name.
func (c *Client) Delete(ctx context.Context, plural, name string) error {
	_, status, err := c.doRaw(ctx, http.MethodDelete, fmt.Sprintf("/v1/%s/%s", plural, name), nil, false)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || status == http.StatusNoContent || (status >= 200 && status < 300) {
		return nil
	}
	return fmt.Errorf("DELETE /v1/%s/%s: status %d", plural, name, status)
}

// WatchEvent is one SSE frame from GET /v1/watch/{plural}.
type WatchEvent struct {
	Type            string   `json:"type"`
	ResourceVersion string   `json:"resourceVersion"`
	Resource        Resource `json:"resource"`
}

// Watch streams SSE events; callback return error stops the watch.
func (c *Client) Watch(ctx context.Context, plural string, since string, fn func(WatchEvent) error) error {
	if since == "" {
		since = "0"
	}
	path := fmt.Sprintf("/v1/watch/%s?since=%s", plural, url.QueryEscape(since))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	// Streaming client — no overall timeout.
	httpClient := &http.Client{Timeout: 0}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("watch status %d: %s", resp.StatusCode, truncate(string(data), 256))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line == "" && len(dataLines) > 0 {
			payload := strings.Join(dataLines, "\n")
			dataLines = nil
			var ev WatchEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			if err := fn(ev); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any, asController bool) (int, error) {
	raw, status, err := c.doRaw(ctx, method, path, body, asController)
	if err != nil {
		return 0, err
	}
	if out != nil && len(raw) > 0 && status >= 200 && status < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, fmt.Errorf("decode response: %w", err)
		}
	}
	return status, nil
}

func (c *Client) doRaw(ctx context.Context, method, path string, body any, asController bool) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if asController {
		ctrl := c.Controller
		if ctrl == "" {
			ctrl = controllerName
		}
		req.Header.Set("X-Forge-Controller", ctrl)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
