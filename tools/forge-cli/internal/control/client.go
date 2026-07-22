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
	verbose func(method, path string, status int, requestID string)
}

// APIError is an error returned by Forge Control.
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

// New creates a typed Control API client.
func New(endpoint string, timeout time.Duration, verbose func(method, path string, status int, requestID string)) (*Client, error) {
	client, err := sharedclient.New(endpoint, timeout)
	if err != nil {
		return nil, err
	}
	return &Client{client: client, verbose: verbose}, nil
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

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
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

	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Control: %w", err)
	}
	defer response.Body.Close()
	if c.verbose != nil {
		c.verbose(method, path, response.StatusCode, response.RequestID)
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
		return &APIError{Status: response.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message, RequestID: requestID}
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
