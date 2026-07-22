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

type errorEnvelope struct {
	Error struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		RequestID string            `json:"requestId"`
		Details   map[string]string `json:"details"`
	} `json:"error"`
}
