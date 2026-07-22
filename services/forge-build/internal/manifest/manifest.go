// Package manifest parses and validates forge.yaml build manifests.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath is the default forge.yaml location relative to a repo root.
const DefaultPath = "forge.yaml"

var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Manifest is the forge.yaml document.
type Manifest struct {
	Service Service `yaml:"service" json:"service"`
	Build   Build   `yaml:"build" json:"build"`
}

// Service identifies the application produced by the build.
type Service struct {
	Name string `yaml:"name" json:"name"`
	Port int    `yaml:"port" json:"port"`
}

// Build describes Docker build inputs relative to the repository root.
type Build struct {
	Dockerfile string `yaml:"dockerfile" json:"dockerfile"`
	Context    string `yaml:"context" json:"context"`
}

// ValidationError is a field-level forge.yaml validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Details returns the shared error-envelope details map.
func (e *ValidationError) Details() map[string]string {
	d := map[string]string{"reason": e.Message}
	if e.Field != "" {
		d["field"] = e.Field
	}
	return d
}

// Parse reads and validates YAML bytes as a forge.yaml manifest.
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Manifest{}, &ValidationError{Field: "", Message: "invalid YAML: " + err.Error()}
	}
	if err := Validate(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// ParseFile reads path and validates it as a forge.yaml manifest.
func ParseFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read forge.yaml: %w", err)
	}
	return Parse(data)
}

// Validate checks required fields, name/port rules, and path-traversal safety.
func Validate(m Manifest) error {
	name := strings.TrimSpace(m.Service.Name)
	if name == "" {
		return &ValidationError{Field: "service.name", Message: "service name is required"}
	}
	if !serviceNamePattern.MatchString(name) {
		return &ValidationError{
			Field:   "service.name",
			Message: "service name must match ^[a-z][a-z0-9-]{0,62}$",
		}
	}
	if m.Service.Port < 1 || m.Service.Port > 65535 {
		return &ValidationError{
			Field:   "service.port",
			Message: "port must be an integer between 1 and 65535",
		}
	}

	dockerfile := strings.TrimSpace(m.Build.Dockerfile)
	if dockerfile == "" {
		return &ValidationError{Field: "build.dockerfile", Message: "dockerfile is required"}
	}
	if err := ValidateRepoRelativePath("build.dockerfile", dockerfile); err != nil {
		return err
	}

	context := strings.TrimSpace(m.Build.Context)
	if context == "" {
		return &ValidationError{Field: "build.context", Message: "context is required"}
	}
	if err := ValidateRepoRelativePath("build.context", context); err != nil {
		return err
	}

	return nil
}

// ResolvePath returns an absolute path under repoRoot for a validated relative path.
// It rejects escapes outside the repository root.
func ResolvePath(repoRoot, rel string) (string, error) {
	if err := ValidateRepoRelativePath("path", rel); err != nil {
		return "", err
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	abs := filepath.Clean(filepath.Join(root, rel))
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", &ValidationError{Field: "path", Message: "path escapes repository root"}
	}
	return abs, nil
}

// ValidateRepoRelativePath rejects absolute paths and repository escapes.
func ValidateRepoRelativePath(field, p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return &ValidationError{Field: field, Message: "path is required"}
	}
	if filepath.IsAbs(p) {
		return &ValidationError{Field: field, Message: "path must be relative to the repository root"}
	}
	// Reject Windows-style absolute paths and drive letters on all platforms.
	if len(p) >= 2 && p[1] == ':' {
		return &ValidationError{Field: field, Message: "path must be relative to the repository root"}
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return &ValidationError{Field: field, Message: "path must not escape the repository root"}
	}
	for _, seg := range strings.Split(filepath.ToSlash(cleaned), "/") {
		if seg == ".." {
			return &ValidationError{Field: field, Message: "path must not escape the repository root"}
		}
	}
	return nil
}

// AsValidationError unwraps err to *ValidationError when possible.
func AsValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}
