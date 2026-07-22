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

	sharedclient "forge.local/tools/forge-cli/internal/client"
)

// Client is a typed client for Forge Control resource endpoints.
type Client struct {
	client  *sharedclient.Client
	token   string
	verbose func(method, path string, status int, requestID string, duration time.Duration)
}

// APIError is an error returned by Forge Control.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	Details   map[string]string
}

func (e *APIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		message = "not logged in or session expired; run forge login"
	case http.StatusForbidden:
		message = "forbidden"
		if action := e.Details["required_action"]; action != "" {
			message = fmt.Sprintf("forbidden: insufficient role for %s", action)
		}
		if role := e.Details["role"]; role != "" {
			message = fmt.Sprintf("%s (current role: %s)", message, role)
		}
	}
	if e.RequestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", message, e.RequestID)
	}
	return message
}

// New creates a typed Control API client.
func New(endpoint string, timeout time.Duration, verbose func(method, path string, status int, requestID string, duration time.Duration)) (*Client, error) {
	client, err := sharedclient.New(endpoint, timeout)
	if err != nil {
		return nil, err
	}
	return &Client{client: client, verbose: verbose}, nil
}

// SetBearerToken configures the Authorization header for subsequent Control calls.
func (c *Client) SetBearerToken(token string) {
	c.token = strings.TrimSpace(token)
}

// CreateProject creates a project.
func (c *Client) CreateProject(ctx context.Context, name, slug string) (Project, error) {
	var project Project
	err := c.doJSON(ctx, http.MethodPost, "/v1/projects", createProjectRequest{Name: name, Slug: slug}, &project)
	return project, err
}

// ListProjects returns all projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	err := c.doJSON(ctx, http.MethodGet, "/v1/projects", nil, &projects)
	return projects, err
}

// GetProject returns a project by ID.
func (c *Client) GetProject(ctx context.Context, id string) (Project, error) {
	var project Project
	err := c.doJSON(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(id), nil, &project)
	return project, err
}

// CreateEnvironment creates an environment under a project.
func (c *Client) CreateEnvironment(ctx context.Context, projectID, name string) (Environment, error) {
	var environment Environment
	err := c.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/environments", createNameRequest{Name: name}, &environment)
	return environment, err
}

// ListEnvironments returns a project's environments.
func (c *Client) ListEnvironments(ctx context.Context, projectID string) ([]Environment, error) {
	var environments []Environment
	err := c.doJSON(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(projectID)+"/environments", nil, &environments)
	return environments, err
}

// CreateApplication creates an application under a project.
func (c *Client) CreateApplication(ctx context.Context, projectID, name string) (Application, error) {
	var application Application
	err := c.doJSON(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(projectID)+"/applications", createNameRequest{Name: name}, &application)
	return application, err
}

// ListApplications returns a project's applications.
func (c *Client) ListApplications(ctx context.Context, projectID string) ([]Application, error) {
	var applications []Application
	err := c.doJSON(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(projectID)+"/applications", nil, &applications)
	return applications, err
}

// CreateService creates a service under an application.
func (c *Client) CreateService(ctx context.Context, applicationID, name string, port int) (Service, error) {
	var service Service
	err := c.doJSON(ctx, http.MethodPost, "/v1/applications/"+url.PathEscape(applicationID)+"/services", createServiceRequest{Name: name, Port: port}, &service)
	return service, err
}

// ListServices returns an application's services.
func (c *Client) ListServices(ctx context.Context, applicationID string) ([]Service, error) {
	var services []Service
	err := c.doJSON(ctx, http.MethodGet, "/v1/applications/"+url.PathEscape(applicationID)+"/services", nil, &services)
	return services, err
}

// CreateDeployment records a desired deployment for a service.
func (c *Client) CreateDeployment(ctx context.Context, serviceID, image string, desiredReplicas int, environmentID, idempotencyKey string) (Deployment, error) {
	var deployment Deployment
	headers := make(http.Header)
	headers.Set("Idempotency-Key", idempotencyKey)
	err := c.doJSONWithHeaders(
		ctx,
		http.MethodPost,
		"/v1/services/"+url.PathEscape(serviceID)+"/deployments",
		createDeploymentRequest{
			Image:           image,
			DesiredReplicas: desiredReplicas,
			EnvironmentID:   environmentID,
		},
		&deployment,
		headers,
	)
	return deployment, err
}

// GetDeployment returns a deployment by ID.
func (c *Client) GetDeployment(ctx context.Context, id string) (Deployment, error) {
	var deployment Deployment
	err := c.doJSON(ctx, http.MethodGet, "/v1/deployments/"+url.PathEscape(id), nil, &deployment)
	return deployment, err
}

// ListDeployments returns a service's deployments.
func (c *Client) ListDeployments(ctx context.Context, serviceID string) ([]Deployment, error) {
	var deployments []Deployment
	err := c.doJSON(ctx, http.MethodGet, "/v1/services/"+url.PathEscape(serviceID)+"/deployments", nil, &deployments)
	return deployments, err
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	return c.doJSONWithHeaders(ctx, method, path, input, output, nil)
}

func (c *Client) doJSONWithHeaders(ctx context.Context, method, path string, input, output any, headers http.Header) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	requestURL := c.client.BaseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	started := time.Now()
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Control: %w", err)
	}
	defer response.Body.Close()
	if c.verbose != nil {
		c.verbose(method, path, response.StatusCode, response.RequestID, time.Since(started))
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var envelope errorEnvelope
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			return &APIError{Status: response.StatusCode, Message: fmt.Sprintf("Control returned HTTP %d", response.StatusCode), RequestID: response.RequestID}
		}
		requestID := envelope.Error.RequestID
		if requestID == "" {
			requestID = response.RequestID
		}
		return &APIError{
			Status:    response.StatusCode,
			Code:      envelope.Error.Code,
			Message:   envelope.Error.Message,
			RequestID: requestID,
			Details:   envelope.Error.Details,
		}
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode Control response: %w", err)
	}
	return nil
}

// IsNetworkError reports whether err describes a failed Control request.
func IsNetworkError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "request Control:")
}
