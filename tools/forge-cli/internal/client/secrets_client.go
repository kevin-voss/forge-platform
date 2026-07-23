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

const defaultSecretsURL = "http://127.0.0.1:4104"

// SecretsClient is a typed client for Forge Secrets secret/config APIs.
type SecretsClient struct {
	http    *http.Client
	baseURL *url.URL
	token   string
	verbose func(method, path string, status int, requestID string, duration time.Duration)
}

// SecretsAPIError is an error returned by Forge Secrets.
type SecretsAPIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *SecretsAPIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		message = "not logged in or session expired; run forge login"
	case http.StatusForbidden:
		if message == "" || message == "forbidden" {
			message = "forbidden: insufficient role or project isolation denied"
		}
	}
	if e.RequestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", message, e.RequestID)
	}
	return message
}

// SecretListItem is metadata returned by GET .../secrets (never includes value).
type SecretListItem struct {
	Name      string `json:"name"`
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// SetSecretResponse is returned by PUT .../secrets/{name}.
type SetSecretResponse struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// ConfigListItem is a plaintext config entry (values are intentionally returned).
type ConfigListItem struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// NewSecretsClient creates a Secrets API client.
func NewSecretsClient(endpoint string, timeout time.Duration, verbose func(method, path string, status int, requestID string, duration time.Duration)) (*SecretsClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultSecretsURL()
	}
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse secrets endpoint: %w", err)
	}
	return &SecretsClient{
		http:    &http.Client{Timeout: timeout},
		baseURL: baseURL,
		verbose: verbose,
	}, nil
}

// DefaultSecretsURL returns FORGE_SECRETS_URL or the local Compose default.
func DefaultSecretsURL() string {
	if u := strings.TrimSpace(os.Getenv("FORGE_SECRETS_URL")); u != "" {
		return u
	}
	return defaultSecretsURL
}

// SetBearerToken configures the Authorization header for subsequent calls.
func (c *SecretsClient) SetBearerToken(token string) {
	c.token = strings.TrimSpace(token)
}

// ListSecrets returns secret metadata for a project/environment (no values).
func (c *SecretsClient) ListSecrets(ctx context.Context, projectID, environment string) ([]SecretListItem, error) {
	var items []SecretListItem
	err := c.doJSON(ctx, http.MethodGet, secretsAPIPath(projectID, environment, ""), nil, &items)
	return items, err
}

// SetSecret creates a new secret version.
func (c *SecretsClient) SetSecret(ctx context.Context, projectID, environment, name, value string) (SetSecretResponse, error) {
	var result SetSecretResponse
	err := c.doJSON(ctx, http.MethodPut, secretsAPIPath(projectID, environment, name), map[string]string{"value": value}, &result)
	return result, err
}

// ListConfig returns non-secret config entries including values.
func (c *SecretsClient) ListConfig(ctx context.Context, projectID, environment string) ([]ConfigListItem, error) {
	var items []ConfigListItem
	err := c.doJSON(ctx, http.MethodGet, configAPIPath(projectID, environment, ""), nil, &items)
	return items, err
}

// SetConfig upserts a plaintext config value.
func (c *SecretsClient) SetConfig(ctx context.Context, projectID, environment, name, value string) (ConfigListItem, error) {
	var result ConfigListItem
	err := c.doJSON(ctx, http.MethodPut, configAPIPath(projectID, environment, name), map[string]string{"value": value}, &result)
	return result, err
}

func secretsAPIPath(projectID, environment, name string) string {
	base := "/v1/projects/" + url.PathEscape(projectID) + "/envs/" + url.PathEscape(environment) + "/secrets"
	if name == "" {
		return base
	}
	return base + "/" + url.PathEscape(name)
}

func configAPIPath(projectID, environment, name string) string {
	base := "/v1/projects/" + url.PathEscape(projectID) + "/envs/" + url.PathEscape(environment) + "/config"
	if name == "" {
		return base
	}
	return base + "/" + url.PathEscape(name)
}

func (c *SecretsClient) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode secrets request: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	requestURL := c.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create secrets request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	started := time.Now()
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request Secrets: %w", err)
	}
	defer response.Body.Close()
	requestID := response.Header.Get("X-Request-Id")
	if c.verbose != nil {
		c.verbose(method, path, response.StatusCode, requestID, time.Since(started))
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeSecretsAPIError(response, requestID)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode Secrets response: %w", err)
	}
	return nil
}

func decodeSecretsAPIError(response *http.Response, requestID string) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var flat struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(body, &flat)
	message := flat.Error
	if message == "" {
		message = fmt.Sprintf("Secrets returned HTTP %d", response.StatusCode)
	}
	return &SecretsAPIError{
		Status:    response.StatusCode,
		Code:      flat.Code,
		Message:   message,
		RequestID: requestID,
	}
}

// IsSecretsNetworkError reports whether err describes a failed Secrets request.
func IsSecretsNetworkError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "request Secrets:")
}
