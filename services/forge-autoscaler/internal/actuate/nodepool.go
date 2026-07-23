package actuate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// NodePoolView is the subset of a cluster-scoped NodePool the node autoscaler needs.
type NodePoolView struct {
	Name            string
	ResourceVersion string
	Generation      int64
	Labels          map[string]string
	Annotations     map[string]string
	Spec            map[string]any
	Status          map[string]any
	Raw             map[string]any
}

// NodePoolClient lists/patches NodePools and writes recommendation status via Control.
type NodePoolClient struct {
	BaseURL    string
	HTTPClient *http.Client
	// StatusController is sent as X-Forge-Controller on status writes.
	// NodePool is registered by forge-infrastructure today; recommendation fields
	// are conceptually owned by the node autoscaler and co-written under that header.
	StatusController string
}

// ErrNodePoolConflict indicates a stale resourceVersion.
var ErrNodePoolConflict = fmt.Errorf("nodepool resource version conflict")

// ErrNodePoolNotFound indicates the pool is missing.
var ErrNodePoolNotFound = fmt.Errorf("nodepool not found")

func (c *NodePoolClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *NodePoolClient) statusController() string {
	if s := strings.TrimSpace(c.StatusController); s != "" {
		return s
	}
	return "forge-infrastructure"
}

// List returns all cluster-scoped NodePools.
func (c *NodePoolClient) List(ctx context.Context) ([]NodePoolView, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("control URL is not configured")
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/nodepools"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list nodepools: status %d: %s", resp.StatusCode, truncate(body))
	}
	trim := bytes.TrimSpace(body)
	if len(trim) > 0 && trim[0] == '[' {
		return nil, nil
	}
	var envelope struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode nodepools: %w", err)
	}
	out := make([]NodePoolView, 0, len(envelope.Items))
	for _, item := range envelope.Items {
		out = append(out, parseNodePool(item))
	}
	return out, nil
}

// Get fetches one NodePool by name.
func (c *NodePoolClient) Get(ctx context.Context, name string) (NodePoolView, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return NodePoolView{}, fmt.Errorf("control URL is not configured")
	}
	endpoint := fmt.Sprintf("%s/v1/nodepools/%s", strings.TrimRight(c.BaseURL, "/"), name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return NodePoolView{}, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return NodePoolView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return NodePoolView{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return NodePoolView{}, ErrNodePoolNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NodePoolView{}, fmt.Errorf("get nodepool: status %d: %s", resp.StatusCode, truncate(body))
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		return NodePoolView{}, err
	}
	return parseNodePool(env), nil
}

// SetReplicas patches NodePool.spec.replicas (Infrastructure creates nodes from this).
// Idempotent when the pool already has the desired count.
func (c *NodePoolClient) SetReplicas(ctx context.Context, name string, replicas int, operationID string) (NodePoolView, error) {
	var last NodePoolView
	for attempt := 0; attempt < 3; attempt++ {
		view, err := c.Get(ctx, name)
		if err != nil {
			return NodePoolView{}, err
		}
		last = view
		if SpecReplicas(view) == replicas {
			return view, nil
		}
		updated, err := c.patchReplicas(ctx, name, view.ResourceVersion, replicas, operationID)
		if err == nil {
			return updated, nil
		}
		if err != ErrNodePoolConflict {
			return NodePoolView{}, err
		}
	}
	return last, ErrNodePoolConflict
}

func (c *NodePoolClient) patchReplicas(ctx context.Context, name, resourceVersion string, replicas int, operationID string) (NodePoolView, error) {
	endpoint := fmt.Sprintf("%s/v1/nodepools/%s", strings.TrimRight(c.BaseURL, "/"), name)
	payload := map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
		},
		"spec": map[string]any{
			"replicas": replicas,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return NodePoolView{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(raw))
	if err != nil {
		return NodePoolView{}, err
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	if operationID != "" {
		req.Header.Set("Idempotency-Key", operationID)
		req.Header.Set("X-Forge-Operation-Id", operationID)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return NodePoolView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return NodePoolView{}, err
	}
	if resp.StatusCode == http.StatusConflict {
		return NodePoolView{}, ErrNodePoolConflict
	}
	if resp.StatusCode == http.StatusNotFound {
		return NodePoolView{}, ErrNodePoolNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NodePoolView{}, fmt.Errorf("patch nodepool: status %d: %s", resp.StatusCode, truncate(body))
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		return NodePoolView{}, err
	}
	return parseNodePool(env), nil
}

// PutStatus writes NodePool status (merge-patch of recommendation + observed fields).
func (c *NodePoolClient) PutStatus(ctx context.Context, name, resourceVersion string, status map[string]any) (NodePoolView, error) {
	endpoint := fmt.Sprintf("%s/v1/nodepools/%s/status", strings.TrimRight(c.BaseURL, "/"), name)
	payload := map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
		},
		"status": status,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return NodePoolView{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(raw))
	if err != nil {
		return NodePoolView{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forge-Controller", c.statusController())
	resp, err := c.client().Do(req)
	if err != nil {
		return NodePoolView{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return NodePoolView{}, err
	}
	if resp.StatusCode == http.StatusConflict {
		return NodePoolView{}, ErrNodePoolConflict
	}
	if resp.StatusCode == http.StatusNotFound {
		return NodePoolView{}, ErrNodePoolNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NodePoolView{}, fmt.Errorf("put nodepool status: status %d: %s", resp.StatusCode, truncate(body))
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		return NodePoolView{}, err
	}
	return parseNodePool(env), nil
}

func parseNodePool(env map[string]any) NodePoolView {
	view := NodePoolView{Raw: env, Labels: map[string]string{}, Annotations: map[string]string{}}
	if meta, ok := env["metadata"].(map[string]any); ok {
		view.Name = asString(meta["name"])
		view.ResourceVersion = asString(meta["resourceVersion"])
		if g, ok := asInt(meta["generation"]); ok {
			view.Generation = int64(g)
		}
		if labels, ok := meta["labels"].(map[string]any); ok {
			for k, v := range labels {
				view.Labels[k] = asString(v)
			}
		}
		if ann, ok := meta["annotations"].(map[string]any); ok {
			for k, v := range ann {
				view.Annotations[k] = asString(v)
			}
		}
	}
	if spec, ok := env["spec"].(map[string]any); ok {
		view.Spec = spec
	} else {
		view.Spec = map[string]any{}
	}
	if status, ok := env["status"].(map[string]any); ok {
		view.Status = status
	} else {
		view.Status = map[string]any{}
	}
	return view
}

// SpecReplicas returns NodePool.spec.replicas (0 if unset).
func SpecReplicas(v NodePoolView) int {
	if n, ok := asInt(v.Spec["replicas"]); ok {
		return n
	}
	return 0
}

// StatusInt reads an int status field.
func StatusInt(v NodePoolView, key string) int {
	if n, ok := asInt(v.Status[key]); ok {
		return n
	}
	return 0
}

// StatusString reads a string status field.
func StatusString(v NodePoolView, key string) string {
	return asString(v.Status[key])
}

// MaxNodes returns the pool ceiling from spec.scaling.maxNodes, spec.maxNodes, or a default.
func MaxNodes(v NodePoolView, fallback int) int {
	if scaling, ok := v.Spec["scaling"].(map[string]any); ok {
		if n, ok := asInt(scaling["maxNodes"]); ok && n > 0 {
			return n
		}
	}
	if n, ok := asInt(v.Spec["maxNodes"]); ok && n > 0 {
		return n
	}
	if fallback > 0 {
		return fallback
	}
	return 100
}

// MinNodes returns the pool floor from spec.scaling.minNodes / spec.minNodes.
// Defaults to 1 so the node autoscaler can scale below the last written
// spec.replicas after a scale-up (operator floor is minNodes, not replicas).
func MinNodes(v NodePoolView) int {
	if scaling, ok := v.Spec["scaling"].(map[string]any); ok {
		if n, ok := asInt(scaling["minNodes"]); ok && n >= 0 {
			return n
		}
	}
	if n, ok := asInt(v.Spec["minNodes"]); ok && n >= 0 {
		return n
	}
	return 1
}

// Priority returns operator-set priority (lower wins). Default 100.
func Priority(v NodePoolView) int {
	if n, ok := asInt(v.Spec["priority"]); ok {
		return n
	}
	return 100
}

// Region returns spec.region.
func Region(v NodePoolView) string {
	return asString(v.Spec["region"])
}

// MachineType returns spec.machineType.
func MachineType(v NodePoolView) string {
	return asString(v.Spec["machineType"])
}

// Architecture returns spec.machine.architecture or labels.
func Architecture(v NodePoolView) string {
	if machine, ok := v.Spec["machine"].(map[string]any); ok {
		if a := asString(machine["architecture"]); a != "" {
			return a
		}
	}
	if a := v.Labels["architecture"]; a != "" {
		return a
	}
	if a := v.Labels["kubernetes.io/arch"]; a != "" {
		return a
	}
	return asString(v.Spec["architecture"])
}

// GPUCount returns requested/available GPUs on the pool (0 if none).
func GPUCount(v NodePoolView) int {
	if machine, ok := v.Spec["machine"].(map[string]any); ok {
		if n, ok := asInt(machine["gpu"]); ok {
			return n
		}
		if n, ok := asInt(machine["gpus"]); ok {
			return n
		}
	}
	if n, ok := asInt(v.Spec["gpu"]); ok {
		return n
	}
	if n, ok := asInt(v.Labels["gpu"]); ok {
		return n
	}
	return 0
}

// MachineSelector returns label requirements from spec.machineSelector or spec.labels.
func MachineSelector(v NodePoolView) map[string]string {
	out := map[string]string{}
	if sel, ok := v.Spec["machineSelector"].(map[string]any); ok {
		for k, val := range sel {
			out[k] = asString(val)
		}
	}
	if labels, ok := v.Spec["labels"].(map[string]any); ok {
		for k, val := range labels {
			if _, exists := out[k]; !exists {
				out[k] = asString(val)
			}
		}
	}
	return out
}

// PoolLabels merges metadata labels and spec.labels for matching.
func PoolLabels(v NodePoolView) map[string]string {
	out := map[string]string{}
	for k, val := range v.Labels {
		out[k] = val
	}
	if labels, ok := v.Spec["labels"].(map[string]any); ok {
		for k, val := range labels {
			out[k] = asString(val)
		}
	}
	return out
}

// HasProviderCapacityBlocked reports InventoryExhausted / ProviderCapacityBlocked conditions.
func HasProviderCapacityBlocked(v NodePoolView) bool {
	conds, ok := v.Status["conditions"].([]any)
	if !ok {
		return false
	}
	for _, raw := range conds {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reason := strings.ToLower(asString(cond["reason"]))
		ctype := strings.ToLower(asString(cond["type"]))
		status := strings.ToLower(asString(cond["status"]))
		if reason == "inventoryexhausted" || reason == "providercapacityblocked" {
			return true
		}
		if ctype == "providercapacityblocked" && status == "true" {
			return true
		}
		if ctype == "available" && status == "false" && strings.Contains(reason, "inventory") {
			return true
		}
	}
	return false
}

// NodePoolActuator is the subset used by the node scale-up loop.
type NodePoolActuator interface {
	List(ctx context.Context) ([]NodePoolView, error)
	Get(ctx context.Context, name string) (NodePoolView, error)
	SetReplicas(ctx context.Context, name string, replicas int, operationID string) (NodePoolView, error)
	PutStatus(ctx context.Context, name, resourceVersion string, status map[string]any) (NodePoolView, error)
}
