package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ScaleDownGuards evaluates voluntary disruption safeguards (25.04 / 25.05).
// Implementations must be fail-closed for stateful primaries and soft-open for
// disruption budgets until those Control APIs ship.
type ScaleDownGuards interface {
	HasActiveDeployment(ctx context.Context) (bool, string, error)
	DisruptionBudgetAllows(ctx context.Context, deploymentHint string) (bool, string, error)
	HasStatefulPrimary(ctx context.Context, node FleetNode) (bool, string, error)
}

// GuardSource probes Control for scale-down safeguards.
type GuardSource struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (g *GuardSource) client() *http.Client {
	if g.HTTPClient != nil {
		return g.HTTPClient
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// HasActiveDeployment reports cluster-wide progressing rollouts when the API exists.
func (g *GuardSource) HasActiveDeployment(ctx context.Context) (bool, string, error) {
	if strings.TrimSpace(g.BaseURL) == "" {
		return false, "", nil
	}
	endpoint := strings.TrimRight(g.BaseURL, "/") + "/v1/deployments?phase=Progressing"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, "", err
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return false, "", err
	}
	// Endpoint not shipped / not supported → do not block scale-down.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		return false, "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "", fmt.Errorf("list progressing deployments: status %d", resp.StatusCode)
	}
	trim := strings.TrimSpace(string(body))
	if trim == "" || trim == "[]" || trim == "{}" {
		return false, "", nil
	}
	var arr []any
	if err := json.Unmarshal(body, &arr); err == nil {
		if len(arr) > 0 {
			return true, "ActiveDeployment", nil
		}
		return false, "", nil
	}
	var envelope struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Items) > 0 {
		return true, "ActiveDeployment", nil
	}
	return false, "", nil
}

// DisruptionBudgetAllows checks a voluntary-removal budget when 25.04 is present.
// Missing API → allow (soft-open); explicit deny → block.
func (g *GuardSource) DisruptionBudgetAllows(ctx context.Context, deploymentHint string) (bool, string, error) {
	if strings.TrimSpace(g.BaseURL) == "" || strings.TrimSpace(deploymentHint) == "" {
		return true, "", nil
	}
	endpoint := fmt.Sprintf("%s/v1/deployments/%s/disruption-budget", strings.TrimRight(g.BaseURL, "/"), deploymentHint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return true, "", err
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return true, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return true, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return true, "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return true, "", nil
	}
	var budget map[string]any
	if err := json.Unmarshal(body, &budget); err != nil {
		return true, "", nil
	}
	// Prefer explicit allow flag from future guard API.
	if allow, ok := budget["allows_voluntary_removal"].(bool); ok {
		if !allow {
			return false, "DisruptionBudgetBlocked", nil
		}
		return true, "", nil
	}
	if allow, ok := budget["allowed"].(bool); ok && !allow {
		return false, "DisruptionBudgetBlocked", nil
	}
	return true, "", nil
}

// HasStatefulPrimary detects protected primaries via labels / replica naming
// until 25.05 structured placement fields land.
func (g *GuardSource) HasStatefulPrimary(_ context.Context, node FleetNode) (bool, string, error) {
	if looksLikeStatefulPrimary(node) {
		return true, "StatefulPrimaryProtected", nil
	}
	return false, "", nil
}

func looksLikeStatefulPrimary(node FleetNode) bool {
	for k, v := range node.Labels {
		lk := strings.ToLower(k)
		lv := strings.ToLower(v)
		if (strings.Contains(lk, "stateful") || strings.Contains(lk, "role")) &&
			(lv == "primary" || lv == "true" || strings.Contains(lv, "primary")) {
			return true
		}
		if lk == "forge.dev/stateful-role" && lv == "primary" {
			return true
		}
	}
	for _, rep := range node.RunningReplicas {
		r := strings.ToLower(rep)
		if strings.Contains(r, "stateful") && strings.Contains(r, "primary") {
			return true
		}
		if strings.HasSuffix(r, "/primary") || strings.Contains(r, ":primary") || strings.Contains(r, "#primary") {
			return true
		}
		if strings.Contains(r, "role=primary") {
			return true
		}
	}
	return false
}

// AllowAllGuards is a test double that never blocks.
type AllowAllGuards struct{}

func (AllowAllGuards) HasActiveDeployment(context.Context) (bool, string, error) {
	return false, "", nil
}
func (AllowAllGuards) DisruptionBudgetAllows(context.Context, string) (bool, string, error) {
	return true, "", nil
}
func (AllowAllGuards) HasStatefulPrimary(context.Context, FleetNode) (bool, string, error) {
	return false, "", nil
}

// StaticGuards returns fixed answers for unit tests.
type StaticGuards struct {
	ActiveDeployment bool
	ActiveReason     string
	BudgetAllows     bool
	BudgetReason     string
	StatefulNodes    map[string]bool
	StatefulReason   string
}

func (s StaticGuards) HasActiveDeployment(context.Context) (bool, string, error) {
	return s.ActiveDeployment, s.ActiveReason, nil
}

func (s StaticGuards) DisruptionBudgetAllows(context.Context, string) (bool, string, error) {
	if s.BudgetAllows {
		return true, "", nil
	}
	reason := s.BudgetReason
	if reason == "" {
		reason = "DisruptionBudgetBlocked"
	}
	return false, reason, nil
}

func (s StaticGuards) HasStatefulPrimary(_ context.Context, node FleetNode) (bool, string, error) {
	if s.StatefulNodes != nil && s.StatefulNodes[node.ID] {
		reason := s.StatefulReason
		if reason == "" {
			reason = "StatefulPrimaryProtected"
		}
		return true, reason, nil
	}
	if looksLikeStatefulPrimary(node) {
		return true, "StatefulPrimaryProtected", nil
	}
	return false, "", nil
}
