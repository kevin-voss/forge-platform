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

// CreateDbInstance provisions a managed database instance for a project.
func (c *Client) CreateDbInstance(ctx context.Context, projectID, name string) (DbInstance, error) {
	var instance DbInstance
	err := c.doJSONWithHeaders(
		ctx,
		http.MethodPost,
		"/v1/databases/instances",
		createDbInstanceRequest{Name: name, ProjectID: projectID},
		&instance,
		projectHeaders(projectID),
	)
	return instance, err
}

// ListDbInstances lists managed database instances for a project.
func (c *Client) ListDbInstances(ctx context.Context, projectID string) ([]DbInstance, error) {
	var instances []DbInstance
	path := "/v1/databases/instances?projectId=" + url.QueryEscape(projectID)
	err := c.doJSONWithHeaders(ctx, http.MethodGet, path, nil, &instances, projectHeaders(projectID))
	return instances, err
}

// GetDbInstance returns a managed database instance by ID.
func (c *Client) GetDbInstance(ctx context.Context, instanceID string) (DbInstance, error) {
	var instance DbInstance
	err := c.doJSON(ctx, http.MethodGet, "/v1/databases/instances/"+url.PathEscape(instanceID), nil, &instance)
	return instance, err
}

// PatchDbInstanceDeletionProtection updates deletion protection on an instance.
func (c *Client) PatchDbInstanceDeletionProtection(ctx context.Context, instanceID string, enabled bool) (DbInstance, error) {
	var instance DbInstance
	err := c.doJSON(
		ctx,
		http.MethodPatch,
		"/v1/databases/instances/"+url.PathEscape(instanceID),
		patchDeletionProtectionRequest{DeletionProtection: enabled},
		&instance,
	)
	return instance, err
}

// DeleteDbInstance deletes a managed database instance (force required when protected).
func (c *Client) DeleteDbInstance(ctx context.Context, instanceID string, force bool) error {
	path := "/v1/databases/instances/" + url.PathEscape(instanceID)
	if force {
		path += "?force=true"
	}
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// CreateDbDatabase creates a database + credentials on an instance.
func (c *Client) CreateDbDatabase(ctx context.Context, instanceID, name string) (DbDatabase, error) {
	var database DbDatabase
	err := c.doJSON(
		ctx,
		http.MethodPost,
		"/v1/databases/instances/"+url.PathEscape(instanceID)+"/databases",
		createDbDatabaseRequest{Name: name},
		&database,
	)
	return database, err
}

// ListDbDatabases lists databases on an instance.
func (c *Client) ListDbDatabases(ctx context.Context, instanceID string) ([]DbDatabase, error) {
	var databases []DbDatabase
	err := c.doJSON(ctx, http.MethodGet, "/v1/databases/instances/"+url.PathEscape(instanceID)+"/databases", nil, &databases)
	return databases, err
}

// GetDbDatabase returns a managed database by ID.
func (c *Client) GetDbDatabase(ctx context.Context, databaseID string) (DbDatabase, error) {
	var database DbDatabase
	err := c.doJSON(ctx, http.MethodGet, "/v1/databases/"+url.PathEscape(databaseID), nil, &database)
	return database, err
}

// PatchDbDatabaseDeletionProtection updates deletion protection on a database.
func (c *Client) PatchDbDatabaseDeletionProtection(ctx context.Context, databaseID string, enabled bool) (DbDatabase, error) {
	var database DbDatabase
	err := c.doJSON(
		ctx,
		http.MethodPatch,
		"/v1/databases/"+url.PathEscape(databaseID),
		patchDeletionProtectionRequest{DeletionProtection: enabled},
		&database,
	)
	return database, err
}

// DeleteDbDatabase deletes a managed database (force required when protected).
func (c *Client) DeleteDbDatabase(ctx context.Context, databaseID string, force bool) error {
	path := "/v1/databases/" + url.PathEscape(databaseID)
	if force {
		path += "?force=true"
	}
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// AttachDbDatabase attaches a database to an application for env injection.
func (c *Client) AttachDbDatabase(ctx context.Context, databaseID, applicationID, envVar string) (DbAttachment, error) {
	var attachment DbAttachment
	body := attachDatabaseRequest{ApplicationID: applicationID}
	if strings.TrimSpace(envVar) != "" {
		body.EnvVar = envVar
	}
	err := c.doJSON(ctx, http.MethodPost, "/v1/databases/"+url.PathEscape(databaseID)+"/attach", body, &attachment)
	return attachment, err
}

// DetachDbAttachment removes a database attachment.
func (c *Client) DetachDbAttachment(ctx context.Context, attachmentID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/v1/databases/attachments/"+url.PathEscape(attachmentID), nil, nil)
}

// ListApplicationDatabases lists attachments for an application.
func (c *Client) ListApplicationDatabases(ctx context.Context, applicationID string) ([]DbAttachment, error) {
	var attachments []DbAttachment
	err := c.doJSON(ctx, http.MethodGet, "/v1/applications/"+url.PathEscape(applicationID)+"/databases", nil, &attachments)
	return attachments, err
}

// CreateDbBackup starts an on-demand backup for a database.
func (c *Client) CreateDbBackup(ctx context.Context, projectID, databaseID string) (DbBackup, error) {
	var backup DbBackup
	err := c.doJSONWithHeaders(
		ctx,
		http.MethodPost,
		"/v1/databases/"+url.PathEscape(databaseID)+"/backups",
		nil,
		&backup,
		projectHeaders(projectID),
	)
	return backup, err
}

// ListDbBackups lists backups for a database.
func (c *Client) ListDbBackups(ctx context.Context, projectID, databaseID string) ([]DbBackup, error) {
	var backups []DbBackup
	err := c.doJSONWithHeaders(
		ctx,
		http.MethodGet,
		"/v1/databases/"+url.PathEscape(databaseID)+"/backups",
		nil,
		&backups,
		projectHeaders(projectID),
	)
	return backups, err
}

// GetDbBackup returns a backup by ID.
func (c *Client) GetDbBackup(ctx context.Context, projectID, databaseID, backupID string) (DbBackup, error) {
	var backup DbBackup
	err := c.doJSONWithHeaders(
		ctx,
		http.MethodGet,
		"/v1/databases/"+url.PathEscape(databaseID)+"/backups/"+url.PathEscape(backupID),
		nil,
		&backup,
		projectHeaders(projectID),
	)
	return backup, err
}

// RestoreDbBackup restores a backup into a target database.
func (c *Client) RestoreDbBackup(ctx context.Context, projectID, backupID, targetDatabaseID string) (RestoreBackupResponse, error) {
	var result RestoreBackupResponse
	err := c.doJSONWithHeaders(
		ctx,
		http.MethodPost,
		"/v1/databases/backups/"+url.PathEscape(backupID)+"/restore",
		restoreBackupRequest{TargetDatabaseID: targetDatabaseID},
		&result,
		projectHeaders(projectID),
	)
	return result, err
}

// RotateDbCredentials rotates credentials for a managed database.
func (c *Client) RotateDbCredentials(ctx context.Context, databaseID string) (RotateCredentialsResponse, error) {
	var result RotateCredentialsResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/databases/"+url.PathEscape(databaseID)+"/rotate-credentials", nil, &result)
	return result, err
}

func projectHeaders(projectID string) http.Header {
	headers := make(http.Header)
	if strings.TrimSpace(projectID) != "" {
		headers.Set("X-Forge-Project", projectID)
	}
	return headers
}

// Apply submits a multi-resource apply (or dry-run) to Control.
func (c *Client) Apply(ctx context.Context, request ApplyRequest) (ApplyResponse, error) {
	var response ApplyResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/apply", request, &response)
	return response, err
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
	ref, err := url.Parse(path)
	if err != nil {
		return fmt.Errorf("parse Control path: %w", err)
	}
	requestURL := c.client.BaseURL.ResolveReference(ref)
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
