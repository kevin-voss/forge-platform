// Package control implements a minimal Forge Control HTTP client for Build.
package control

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

// RecordImageRequest is POST /v1/services/{serviceId}/image.
type RecordImageRequest struct {
	Image   string `json:"image"`
	Digest  string `json:"digest,omitempty"`
	Commit  string `json:"commit,omitempty"`
	BuildID string `json:"buildId,omitempty"`
}

// RecordImageResponse is the Control service after recording an image.
type RecordImageResponse struct {
	ID           string `json:"id"`
	Image        string `json:"image"`
	ImageDigest  string `json:"imageDigest,omitempty"`
	ImageCommit  string `json:"imageCommit,omitempty"`
	ImageBuildID string `json:"imageBuildId,omitempty"`
}

// CreateDeploymentRequest is POST /v1/services/{serviceId}/deployments.
type CreateDeploymentRequest struct {
	Image           string `json:"image"`
	EnvironmentID   string `json:"environmentId"`
	DesiredReplicas *int   `json:"desiredReplicas,omitempty"`
}

// DeploymentResponse is a Control deployment.
type DeploymentResponse struct {
	ID            string `json:"id"`
	ServiceID     string `json:"serviceId"`
	EnvironmentID string `json:"environmentId"`
	Image         string `json:"image"`
	Status        string `json:"status"`
}

// ImageIdempotencyKey returns the Idempotency-Key for recording a build image.
func ImageIdempotencyKey(buildID string) string {
	return "build-" + strings.TrimSpace(buildID)
}

// DeployIdempotencyKey returns the Idempotency-Key for auto-deploy from a build.
func DeployIdempotencyKey(buildID string) string {
	return "deploy-" + strings.TrimSpace(buildID)
}

// Client talks to Forge Control.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New returns a Control client. baseURL may be empty (disabled).
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTPClient: httpClient,
	}
}

// Enabled reports whether a Control base URL is configured.
func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.BaseURL) != ""
}

// RecordImage posts the built image onto the target Control service.
func (c *Client) RecordImage(ctx context.Context, serviceID string, req RecordImageRequest) (RecordImageResponse, error) {
	if !c.Enabled() {
		return RecordImageResponse{}, fmt.Errorf("control client disabled")
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return RecordImageResponse{}, fmt.Errorf("serviceId is required")
	}
	if strings.TrimSpace(req.Image) == "" {
		return RecordImageResponse{}, fmt.Errorf("image is required")
	}
	path := "/v1/services/" + url.PathEscape(serviceID) + "/image"
	var out RecordImageResponse
	if err := c.postJSON(ctx, path, ImageIdempotencyKey(req.BuildID), req, &out); err != nil {
		return RecordImageResponse{}, err
	}
	return out, nil
}

// CreateDeployment creates a desired-state deployment for the service.
func (c *Client) CreateDeployment(ctx context.Context, serviceID, buildID string, req CreateDeploymentRequest) (DeploymentResponse, error) {
	if !c.Enabled() {
		return DeploymentResponse{}, fmt.Errorf("control client disabled")
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return DeploymentResponse{}, fmt.Errorf("serviceId is required")
	}
	if strings.TrimSpace(req.Image) == "" {
		return DeploymentResponse{}, fmt.Errorf("image is required")
	}
	if strings.TrimSpace(req.EnvironmentID) == "" {
		return DeploymentResponse{}, fmt.Errorf("environmentId is required")
	}
	path := "/v1/services/" + url.PathEscape(serviceID) + "/deployments"
	var out DeploymentResponse
	if err := c.postJSON(ctx, path, DeployIdempotencyKey(buildID), req, &out); err != nil {
		return DeploymentResponse{}, err
	}
	return out, nil
}

func (c *Client) postJSON(ctx context.Context, path, idempotencyKey string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		req.Header.Set("Idempotency-Key", key)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("control request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read control response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode control response: %w", err)
	}
	return nil
}

// HTTPError is a non-2xx Control response.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("control HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("control HTTP %d: %s", e.StatusCode, e.Body)
}

// Transient reports whether the error should be retried.
func Transient(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if asHTTP(err, &httpErr) {
		if httpErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		return httpErr.StatusCode >= 500
	}
	// Network / context deadline / temporary DNS, etc.
	return true
}

func asHTTP(err error, target **HTTPError) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if e, ok := err.(*HTTPError); ok {
			*target = e
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
