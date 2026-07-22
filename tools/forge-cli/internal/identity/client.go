// Package identity provides a thin HTTP client for Forge Identity auth APIs.
package identity

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

	"forge.local/tools/forge-cli/internal/auth"
	"forge.local/tools/forge-cli/internal/config"
)

// Client talks to Forge Identity.
type Client struct {
	http    *http.Client
	baseURL *url.URL
}

// APIError is an error returned by Forge Identity.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *APIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	if e.RequestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", message, e.RequestID)
	}
	return message
}

// LoginResult is returned by POST /v1/auth/login.
type LoginResult struct {
	SessionToken string `json:"session_token"`
	ExpiresAt    string `json:"expires_at"`
}

// ProjectMembership is one project role from introspection.
type ProjectMembership struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	OrgID       string `json:"org_id"`
	Role        string `json:"role"`
}

// OrgMembership is one org role from introspection.
type OrgMembership struct {
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	Role    string `json:"role"`
}

// Memberships groups org and project memberships.
type Memberships struct {
	Orgs     []OrgMembership     `json:"orgs"`
	Projects []ProjectMembership `json:"projects"`
}

// IntrospectResult is returned by POST /v1/auth/introspect.
type IntrospectResult struct {
	Active        bool         `json:"active"`
	PrincipalType string       `json:"principal_type,omitempty"`
	PrincipalID   string       `json:"principal_id,omitempty"`
	UserID        string       `json:"user_id,omitempty"`
	ProjectID     string       `json:"project_id,omitempty"`
	Role          string       `json:"role,omitempty"`
	Memberships   *Memberships `json:"memberships,omitempty"`
}

// New creates an Identity client.
func New(endpoint string, timeout time.Duration) (*Client, error) {
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse identity endpoint: %w", err)
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: baseURL,
	}, nil
}

// Login exchanges email/password for a session token.
func (c *Client) Login(ctx context.Context, email, password string) (LoginResult, error) {
	var result LoginResult
	err := c.doJSON(ctx, http.MethodPost, "/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, "", &result)
	if err != nil {
		return LoginResult{}, err
	}
	if strings.TrimSpace(result.SessionToken) == "" {
		return LoginResult{}, &auth.Error{Message: "identity login returned an empty session token"}
	}
	return result, nil
}

// Introspect validates a token and returns principal details.
func (c *Client) Introspect(ctx context.Context, token string) (IntrospectResult, error) {
	var result IntrospectResult
	err := c.doJSON(ctx, http.MethodPost, "/v1/auth/introspect", map[string]string{
		"token": token,
	}, "", &result)
	return result, err
}

// Logout revokes a session token server-side.
func (c *Client) Logout(ctx context.Context, token string) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/auth/logout", nil, token, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, input any, bearer string, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode identity request: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	requestURL := c.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create identity request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request Identity: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(response)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode identity response: %w", err)
	}
	return nil
}

func decodeAPIError(response *http.Response) error {
	requestID := response.Header.Get("X-Request-Id")
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return &APIError{
			Status:    response.StatusCode,
			Message:   fmt.Sprintf("Identity returned HTTP %d", response.StatusCode),
			RequestID: requestID,
		}
	}
	if envelope.Error.RequestID != "" {
		requestID = envelope.Error.RequestID
	}
	message := envelope.Error.Message
	if response.StatusCode == http.StatusUnauthorized {
		message = "invalid credentials"
	}
	return &APIError{
		Status:    response.StatusCode,
		Code:      envelope.Error.Code,
		Message:   message,
		RequestID: requestID,
	}
}

// IsNetworkError reports whether err describes a failed Identity request.
func IsNetworkError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "request Identity:")
}
