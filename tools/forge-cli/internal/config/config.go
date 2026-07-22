// Package config loads and resolves Forge CLI configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultEndpoint = "http://127.0.0.1:4001"

// File is the on-disk CLI configuration.
type File struct {
	CurrentProfile string             `yaml:"current_profile,omitempty"`
	Profiles       map[string]Profile `yaml:"profiles,omitempty"`
}

// Profile contains settings for one named Control endpoint.
type Profile struct {
	Endpoint string `yaml:"endpoint"`
}

// Resolved is the effective configuration after applying precedence.
type Resolved struct {
	Endpoint string
	Profile  string
}

// UsageError represents input that should produce CLI exit code 2.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

// Path returns the XDG-compatible configuration file path.
func Path() (string, error) {
	if configHome := os.Getenv("XDG_CONFIG_HOME"); configHome != "" {
		return filepath.Join(configHome, "forge", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "forge", "config.yaml"), nil
}

// Load returns an empty configuration when path does not yet exist.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{Profiles: make(map[string]Profile)}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read config %q: %w", path, err)
	}

	var file File
	if err := yaml.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if file.Profiles == nil {
		file.Profiles = make(map[string]Profile)
	}
	return file, nil
}

// Save writes config atomically with permissions suitable for future tokens.
func Save(path string, file File) error {
	if file.Profiles == nil {
		file.Profiles = make(map[string]Profile)
	}
	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// ValidateEndpoint accepts only absolute HTTP(S) URLs.
func ValidateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return &UsageError{Message: fmt.Sprintf("invalid endpoint %q: expected an absolute http(s) URL", endpoint)}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return &UsageError{Message: fmt.Sprintf("invalid endpoint %q: scheme must be http or https", endpoint)}
	}
	if parsed.User != nil {
		return &UsageError{Message: fmt.Sprintf("invalid endpoint %q: credentials are not allowed", endpoint)}
	}
	return nil
}

// Resolve applies endpoint/profile precedence: flag, environment, file, default.
func Resolve(file File, flagEndpoint, flagProfile, envEndpoint, envProfile string) (Resolved, error) {
	profile := firstNonEmpty(flagProfile, envProfile, file.CurrentProfile, "local")
	endpoint := firstNonEmpty(flagEndpoint, envEndpoint)
	if endpoint == "" {
		selected, exists := file.Profiles[profile]
		if exists {
			endpoint = selected.Endpoint
		} else if flagProfile != "" || envProfile != "" || file.CurrentProfile != "" {
			return Resolved{}, &UsageError{Message: fmt.Sprintf("unknown profile %q", profile)}
		}
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if err := ValidateEndpoint(endpoint); err != nil {
		return Resolved{}, err
	}
	return Resolved{Endpoint: endpoint, Profile: profile}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
