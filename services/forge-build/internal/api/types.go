// Package api defines Build HTTP DTOs and shared error-envelope helpers.
package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"forge.local/services/forge-build/internal/manifest"
)

// BuildStatus is the lifecycle state of a build job.
type BuildStatus string

const (
	BuildStatusQueued    BuildStatus = "queued"
	BuildStatusRunning   BuildStatus = "running"
	BuildStatusSucceeded BuildStatus = "succeeded"
	BuildStatusFailed    BuildStatus = "failed"
)

// BuildRequest is the POST /v1/builds body.
type BuildRequest struct {
	Repo          string `json:"repo"`
	Ref           string `json:"ref"`
	ForgeYamlPath string `json:"forgeYamlPath,omitempty"`
}

// BuildAccepted is the 202 response from POST /v1/builds.
type BuildAccepted struct {
	BuildID string      `json:"buildId"`
	Status  BuildStatus `json:"status"`
}

// BuildRecord is the GET /v1/builds/{id} response.
type BuildRecord struct {
	BuildID    string      `json:"buildId"`
	Status     BuildStatus `json:"status"`
	Image      string      `json:"image,omitempty"`
	Commit     string      `json:"commit,omitempty"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// Validate checks required fields and rejects path traversal in forgeYamlPath.
func (r BuildRequest) Validate(defaultForgeYAML string) error {
	if strings.TrimSpace(r.Repo) == "" {
		return &manifest.ValidationError{Field: "repo", Message: "repo is required"}
	}
	if strings.TrimSpace(r.Ref) == "" {
		return &manifest.ValidationError{Field: "ref", Message: "ref is required"}
	}
	path := r.EffectiveForgeYAMLPath(defaultForgeYAML)
	if err := manifest.ValidateRepoRelativePath("forgeYamlPath", path); err != nil {
		return err
	}
	return nil
}

// EffectiveForgeYAMLPath returns the forge.yaml path to use for this request.
func (r BuildRequest) EffectiveForgeYAMLPath(defaultForgeYAML string) string {
	path := strings.TrimSpace(r.ForgeYamlPath)
	if path != "" {
		return path
	}
	path = strings.TrimSpace(defaultForgeYAML)
	if path != "" {
		return path
	}
	return manifest.DefaultPath
}

// DecodeBuildRequest unmarshals and validates a build create body.
func DecodeBuildRequest(data []byte, defaultForgeYAML string) (BuildRequest, error) {
	var req BuildRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return BuildRequest{}, &manifest.ValidationError{
			Field:   "",
			Message: "malformed JSON body: " + err.Error(),
		}
	}
	if err := req.Validate(defaultForgeYAML); err != nil {
		return BuildRequest{}, err
	}
	return req, nil
}

// Envelope is the platform HTTP error shape ({"error":{...}}).
type Envelope struct {
	Error Body `json:"error"`
}

// Body is the inner error object.
type Body struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"requestId"`
}

// ValidationEnvelope builds a 400 validation_error envelope from err.
func ValidationEnvelope(err error, requestID string) Envelope {
	code := "validation_error"
	message := "validation failed"
	var details map[string]string
	if ve, ok := manifest.AsValidationError(err); ok {
		message = ve.Error()
		details = ve.Details()
	} else if err != nil {
		message = err.Error()
	}
	if requestID == "" {
		requestID = "req_unknown"
	}
	return Envelope{
		Error: Body{
			Code:      code,
			Message:   message,
			Details:   details,
			RequestID: requestID,
		},
	}
}

// MustMarshalJSON is a test helper that panics on marshal failure.
func MustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal: %v", err))
	}
	return b
}
