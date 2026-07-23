// Package control defines Forge Control API request and response types.
package control

// Project is a Forge project returned by Control.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Environment is a project environment returned by Control.
type Environment struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Application is a project application returned by Control.
type Application struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Service is an application service returned by Control.
type Service struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
	Port          int    `json:"port"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// Deployment is a desired service deployment returned by Control.
type Deployment struct {
	ID              string `json:"id"`
	ServiceID       string `json:"serviceId"`
	EnvironmentID   string `json:"environmentId"`
	Image           string `json:"image"`
	DesiredReplicas int    `json:"desiredReplicas"`
	Status          string `json:"status"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type createProjectRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

type createNameRequest struct {
	Name string `json:"name"`
}

type createServiceRequest struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type createDeploymentRequest struct {
	Image           string `json:"image"`
	DesiredReplicas int    `json:"desiredReplicas"`
	EnvironmentID   string `json:"environmentId"`
}

// DbInstance is a managed PostgreSQL instance.
type DbInstance struct {
	ID                 string `json:"id"`
	ProjectID          string `json:"projectId"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	Engine             string `json:"engine"`
	DeletionProtection bool   `json:"deletionProtection"`
	StatusReason       string `json:"statusReason,omitempty"`
	EndpointRef        string `json:"endpointRef,omitempty"`
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	ContainerID        string `json:"containerId,omitempty"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

// DbDatabase is a managed database on an instance.
type DbDatabase struct {
	ID                 string `json:"id"`
	InstanceID         string `json:"instanceId"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	StatusReason       string `json:"statusReason,omitempty"`
	DeletionProtection bool   `json:"deletionProtection"`
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	SecretRef          string `json:"secretRef,omitempty"`
	Username           string `json:"username,omitempty"`
	// Password is present only on create/rotate (one-time reveal).
	Password  string `json:"password,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// DbAttachment binds a managed database to an application for env injection.
type DbAttachment struct {
	ID            string `json:"id"`
	DatabaseID    string `json:"databaseId"`
	ApplicationID string `json:"applicationId"`
	EnvVar        string `json:"envVar"`
	SecretRef     string `json:"secretRef,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

// DatabaseListItem is a flattened project database row for CLI list output.
type DatabaseListItem struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	InstanceID         string `json:"instanceId"`
	InstanceName       string `json:"instanceName"`
	Status             string `json:"status"`
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	SecretRef          string `json:"secretRef,omitempty"`
	DeletionProtection bool   `json:"deletionProtection"`
}

// DbBackup is an on-demand managed-database backup.
type DbBackup struct {
	ID                     string `json:"id"`
	DatabaseID             string `json:"databaseId"`
	Status                 string `json:"status"`
	Location               string `json:"location,omitempty"`
	Checksum               string `json:"checksum,omitempty"`
	SizeBytes              int64  `json:"sizeBytes,omitempty"`
	StatusReason           string `json:"statusReason,omitempty"`
	CompletedAt            string `json:"completedAt,omitempty"`
	RestoreStatus          string `json:"restoreStatus,omitempty"`
	RestoreTargetDatabaseID string `json:"restoreTargetDatabaseId,omitempty"`
	RestoreStatusReason    string `json:"restoreStatusReason,omitempty"`
	RestoreCompletedAt     string `json:"restoreCompletedAt,omitempty"`
	CreatedAt              string `json:"createdAt"`
}

// RestoreBackupResponse is returned when a restore is accepted.
type RestoreBackupResponse struct {
	BackupID         string `json:"backupId"`
	TargetDatabaseID string `json:"targetDatabaseId"`
	Status           string `json:"status"`
	StatusReason     string `json:"statusReason,omitempty"`
}

// RotateCredentialsResponse is returned from credential rotation.
type RotateCredentialsResponse struct {
	Credential RotatedCredential `json:"credential"`
	SecretRef  string            `json:"secretRef"`
}

// RotatedCredential is the one-time reveal of a rotated role password.
type RotatedCredential struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	Status    string `json:"status"`
	SecretRef string `json:"secretRef,omitempty"`
	CreatedAt string `json:"createdAt"`
	RotatedAt string `json:"rotatedAt,omitempty"`
}

type createDbInstanceRequest struct {
	Name      string `json:"name"`
	ProjectID string `json:"projectId,omitempty"`
}

type createDbDatabaseRequest struct {
	Name string `json:"name"`
}

type attachDatabaseRequest struct {
	ApplicationID string `json:"applicationId"`
	EnvVar        string `json:"envVar,omitempty"`
}

type patchDeletionProtectionRequest struct {
	DeletionProtection bool `json:"deletionProtection"`
}

type restoreBackupRequest struct {
	TargetDatabaseID string `json:"targetDatabaseId"`
}

// ApplyRequest is the body for POST /v1/apply.
type ApplyRequest struct {
	DryRun    bool             `json:"dryRun"`
	Resources []map[string]any `json:"resources"`
}

// ApplyResponse is returned by POST /v1/apply.
type ApplyResponse struct {
	OperationID  string               `json:"operationId"`
	DryRun       bool                 `json:"dryRun"`
	ChangedCount int                  `json:"changedCount"`
	Results      []ApplyResourceResult `json:"results"`
}

// ApplyResourceResult describes one resource in an apply response.
type ApplyResourceResult struct {
	Kind        string         `json:"kind"`
	Name        string         `json:"name"`
	Action      string         `json:"action"`
	Project     string         `json:"project,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Resource    map[string]any `json:"resource,omitempty"`
	Message     string         `json:"message,omitempty"`
}

type errorEnvelope struct {
	Error struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		RequestID string            `json:"requestId"`
		Details   map[string]string `json:"details"`
	} `json:"error"`
}
