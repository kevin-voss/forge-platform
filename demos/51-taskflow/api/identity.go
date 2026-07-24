package main

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

type introspectResult struct {
	Active        bool   `json:"active"`
	PrincipalType string `json:"principal_type"`
	PrincipalID   string `json:"principal_id"`
	UserID        string `json:"user_id"`
	ProjectID     string `json:"project_id"`
	Role          string `json:"role"`
}

// IdentityClient talks to forge-identity (register/login/introspect/tokens/members).
type IdentityClient interface {
	Introspect(ctx context.Context, token string) (introspectResult, error)
	Register(ctx context.Context, email, password, displayName string) (userID string, err error)
	Login(ctx context.Context, email, password string) (sessionToken string, err error)
	AddProjectMember(ctx context.Context, projectID, userID, role string) error
	CreatePAT(ctx context.Context, ownerID, projectID, role string) (token string, err error)
}

type httpIdentityClient struct {
	baseURL    string
	httpClient *http.Client
}

func newHTTPIdentityClient(baseURL string) *httpIdentityClient {
	return &httpIdentityClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *httpIdentityClient) Introspect(ctx context.Context, token string) (introspectResult, error) {
	var out introspectResult
	if err := c.postJSON(ctx, "/v1/auth/introspect", map[string]string{"token": token}, &out); err != nil {
		return introspectResult{}, err
	}
	return out, nil
}

func (c *httpIdentityClient) Register(ctx context.Context, email, password, displayName string) (string, error) {
	var out struct {
		UserID string `json:"user_id"`
	}
	if err := c.postJSON(ctx, "/v1/auth/register", map[string]string{
		"email":        email,
		"password":     password,
		"display_name": displayName,
	}, &out); err != nil {
		return "", err
	}
	if out.UserID == "" {
		return "", fmt.Errorf("register: empty user_id")
	}
	return out.UserID, nil
}

func (c *httpIdentityClient) Login(ctx context.Context, email, password string) (string, error) {
	var out struct {
		SessionToken string `json:"session_token"`
	}
	if err := c.postJSON(ctx, "/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, &out); err != nil {
		return "", err
	}
	if out.SessionToken == "" {
		return "", fmt.Errorf("login: empty session_token")
	}
	return out.SessionToken, nil
}

func (c *httpIdentityClient) AddProjectMember(ctx context.Context, projectID, userID, role string) error {
	status, body, err := c.doJSON(ctx, http.MethodPost, "/v1/projects/"+projectID+"/members", map[string]string{
		"user_id": userID,
		"role":    role,
	})
	if err != nil {
		return err
	}
	// 201 created; 409/200 if already a member — treat as success for idempotent signup.
	if status == http.StatusCreated || status == http.StatusOK || status == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("add member HTTP %d: %s", status, truncate(body, 200))
}

func (c *httpIdentityClient) CreatePAT(ctx context.Context, ownerID, projectID, role string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	payload := map[string]any{
		"owner":      map[string]string{"type": "user", "id": ownerID},
		"project_id": projectID,
		"role":       role,
	}
	if err := c.postJSON(ctx, "/v1/tokens", payload, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("create token: empty token")
	}
	return out.Token, nil
}

func (c *httpIdentityClient) postJSON(ctx context.Context, path string, payload any, dest any) error {
	status, body, err := c.doJSON(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%s HTTP %d: %s", path, status, truncate(body, 200))
	}
	if dest == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (c *httpIdentityClient) doJSON(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
