package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"forge.local/tools/forge-cli/internal/config"
)

const defaultAgentsURL = "http://127.0.0.1:4301"

// AgentsClient is a thin HTTP client for forge-agents.
type AgentsClient struct {
	http    *http.Client
	baseURL *url.URL
	verbose func(method, path string, status int, requestID string, duration time.Duration)
}

// AgentsAPIError is an error returned by forge-agents.
type AgentsAPIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *AgentsAPIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	if e.RequestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", message, e.RequestID)
	}
	return message
}

// AgentLimits are hard execution ceilings from an agent definition.
type AgentLimits struct {
	MaxSteps        int `json:"max_steps"`
	TimeoutSeconds  int `json:"timeout_seconds"`
}

// AgentInfo is a registry entry from GET /v1/agents.
type AgentInfo struct {
	Name        string      `json:"name"`
	Model       string      `json:"model"`
	Tools       []string    `json:"tools"`
	Permissions []string    `json:"permissions"`
	Limits      AgentLimits `json:"limits"`
}

// AgentListResponse is GET /v1/agents.
type AgentListResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// StartRunResponse is POST /v1/agents/{name}/runs.
type StartRunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// PendingApproval is embedded on a run when status is awaiting_approval.
type PendingApproval struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Status    string         `json:"status"`
	CreatedAt string         `json:"created_at"`
	ExpiresAt string         `json:"expires_at"`
}

// RunDetail is GET /v1/runs/{id}.
type RunDetail struct {
	ID              string           `json:"run_id"`
	Agent           string           `json:"agent"`
	ProjectID       string           `json:"project_id"`
	Status          string           `json:"status"`
	Input           string           `json:"input,omitempty"`
	Result          string           `json:"result,omitempty"`
	Error           string           `json:"error,omitempty"`
	Steps           []map[string]any `json:"steps,omitempty"`
	PendingApproval *PendingApproval `json:"pending_approval,omitempty"`
	StartedAt       string           `json:"started_at,omitempty"`
	EndedAt         string           `json:"ended_at,omitempty"`
}

// ApprovalDecisionResponse is POST /v1/approvals/{id}/approve|deny.
type ApprovalDecisionResponse struct {
	Status string `json:"status"`
}

// NewAgentsClient creates a forge-agents API client.
func NewAgentsClient(endpoint string, timeout time.Duration, verbose func(method, path string, status int, requestID string, duration time.Duration)) (*AgentsClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultAgentsURL()
	}
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse agents endpoint: %w", err)
	}
	return &AgentsClient{
		http:    &http.Client{Timeout: timeout},
		baseURL: baseURL,
		verbose: verbose,
	}, nil
}

// DefaultAgentsURL returns FORGE_AGENTS_URL or the local Compose default.
func DefaultAgentsURL() string {
	if u := strings.TrimSpace(os.Getenv("FORGE_AGENTS_URL")); u != "" {
		return u
	}
	return defaultAgentsURL
}

// ListAgents calls GET /v1/agents.
func (c *AgentsClient) ListAgents(ctx context.Context) (*AgentListResponse, error) {
	var out AgentListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/agents", "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartRun calls POST /v1/agents/{name}/runs.
func (c *AgentsClient) StartRun(ctx context.Context, projectID, agentName, input string, context map[string]any) (*StartRunResponse, error) {
	path := "/v1/agents/" + url.PathEscape(agentName) + "/runs"
	body := map[string]any{"input": input, "context": context}
	var out StartRunResponse
	if err := c.doJSON(ctx, http.MethodPost, path, projectID, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRun calls GET /v1/runs/{id}.
func (c *AgentsClient) GetRun(ctx context.Context, projectID, runID string) (*RunDetail, error) {
	path := "/v1/runs/" + url.PathEscape(runID)
	var out RunDetail
	if err := c.doJSON(ctx, http.MethodGet, path, projectID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApproveApproval calls POST /v1/approvals/{id}/approve.
func (c *AgentsClient) ApproveApproval(ctx context.Context, projectID, approvalID, actor string) (*ApprovalDecisionResponse, error) {
	path := "/v1/approvals/" + url.PathEscape(approvalID) + "/approve"
	var out ApprovalDecisionResponse
	if err := c.doJSONWithActor(ctx, http.MethodPost, path, projectID, actor, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DenyApproval calls POST /v1/approvals/{id}/deny.
func (c *AgentsClient) DenyApproval(ctx context.Context, projectID, approvalID, actor, reason string) (*ApprovalDecisionResponse, error) {
	path := "/v1/approvals/" + url.PathEscape(approvalID) + "/deny"
	body := map[string]any{}
	if strings.TrimSpace(reason) != "" {
		body["reason"] = reason
	}
	var out ApprovalDecisionResponse
	if err := c.doJSONWithActor(ctx, http.MethodPost, path, projectID, actor, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *AgentsClient) doJSON(ctx context.Context, method, path, projectID string, body any, out any) error {
	return c.doJSONWithActor(ctx, method, path, projectID, "", body, out)
}

func (c *AgentsClient) doJSONWithActor(ctx context.Context, method, path, projectID, actor string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL.ResolveReference(&url.URL{Path: path}).String(), reader)
	if err != nil {
		return fmt.Errorf("request Agents: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if projectID != "" {
		req.Header.Set("X-Forge-Project", projectID)
	}
	if actor != "" {
		req.Header.Set("X-Forge-Actor", actor)
	}

	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request Agents: %w", err)
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = resp.Header.Get("X-Request-Id")
	}
	if c.verbose != nil {
		c.verbose(method, path, resp.StatusCode, requestID, time.Since(started))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		apiErr := &AgentsAPIError{Status: resp.StatusCode, RequestID: requestID, Message: strings.TrimSpace(string(raw))}
		var parsed struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			if parsed.Error != "" {
				apiErr.Message = parsed.Error
			}
			apiErr.Code = parsed.Code
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode agents response: %w", err)
	}
	return nil
}

// IsAgentsNetworkError reports whether err describes a failed Agents request.
func IsAgentsNetworkError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "request Agents:")
}
