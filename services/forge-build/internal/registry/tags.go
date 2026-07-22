// Package registry computes image references and pushes builds to the local OCI registry.
package registry

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	DefaultRegistry         = "localhost:5000"
	DefaultImageNamePattern = "{project}-{service}"
	DefaultPushRetries      = 3
)

var (
	unsafeNameChars = regexp.MustCompile(`[^a-z0-9._-]`)
	multiHyphen     = regexp.MustCompile(`-+`)
)

// TagInput is the deterministic inputs for a registry image reference.
type TagInput struct {
	Registry   string
	Pattern    string
	Project    string
	Service    string
	Commit     string
	BuildID    string
	PushLatest bool
}

// Refs holds the versioned (and optional latest) image references for a build.
type Refs struct {
	Repository string
	Tag        string
	Versioned  string
	Latest     string // empty when PushLatest is false
}

// ComputeRefs builds sanitized registry references encoding commit + build id.
func ComputeRefs(in TagInput) (Refs, error) {
	registry := strings.TrimSpace(in.Registry)
	if registry == "" {
		registry = DefaultRegistry
	}
	registry = strings.TrimSuffix(registry, "/")
	if strings.Contains(registry, "://") {
		return Refs{}, fmt.Errorf("registry must be host[:port] without scheme, got %q", in.Registry)
	}

	service := SanitizeComponent(in.Service)
	if service == "" {
		return Refs{}, fmt.Errorf("service name is required for image tagging")
	}
	project := SanitizeComponent(in.Project)

	pattern := strings.TrimSpace(in.Pattern)
	if pattern == "" {
		pattern = DefaultImageNamePattern
	}
	repoName := applyPattern(pattern, project, service)
	if repoName == "" {
		return Refs{}, fmt.Errorf("image name pattern %q produced an empty repository name", pattern)
	}
	if err := validateRepositoryName(repoName); err != nil {
		return Refs{}, err
	}

	commit := strings.TrimSpace(in.Commit)
	if commit == "" {
		return Refs{}, fmt.Errorf("commit is required for image tagging")
	}
	buildID := strings.TrimSpace(in.BuildID)
	if buildID == "" {
		return Refs{}, fmt.Errorf("build id is required for image tagging")
	}

	tag := ShortSHA(commit) + "-" + ShortBuildID(buildID)
	if err := validateTag(tag); err != nil {
		return Refs{}, err
	}

	versioned := registry + "/" + repoName + ":" + tag
	refs := Refs{
		Repository: registry + "/" + repoName,
		Tag:        tag,
		Versioned:  versioned,
	}
	if in.PushLatest {
		refs.Latest = registry + "/" + repoName + ":latest"
	}
	return refs, nil
}

// ShortSHA returns the first 7 characters of a commit SHA (git convention).
func ShortSHA(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// ShortBuildID returns a registry-safe short build id (UUID prefix).
func ShortBuildID(buildID string) string {
	buildID = strings.TrimSpace(buildID)
	if i := strings.IndexByte(buildID, '-'); i > 0 {
		return buildID[:i]
	}
	if len(buildID) > 8 {
		return buildID[:8]
	}
	return buildID
}

// SanitizeComponent lowercases and strips characters unsafe for OCI repository names.
// Empty input remains empty (so optional project can be omitted from the pattern).
func SanitizeComponent(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	s = unsafeNameChars.ReplaceAllString(s, "-")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	return s
}

func applyPattern(pattern, project, service string) string {
	name := strings.ReplaceAll(pattern, "{project}", project)
	name = strings.ReplaceAll(name, "{service}", service)
	name = multiHyphen.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-./")
	return name
}

func validateRepositoryName(name string) error {
	if name == "" {
		return fmt.Errorf("repository name is empty")
	}
	if strings.Contains(name, "{") || strings.Contains(name, "}") {
		return fmt.Errorf("repository name %q still contains pattern tokens", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") {
			return fmt.Errorf("invalid repository name %q", name)
		}
		if unsafeNameChars.MatchString(part) {
			return fmt.Errorf("repository name %q contains unsafe characters", name)
		}
	}
	return nil
}

func validateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag is empty")
	}
	if len(tag) > 128 {
		return fmt.Errorf("tag exceeds 128 characters")
	}
	if unsafeNameChars.MatchString(tag) {
		return fmt.Errorf("tag %q contains unsafe characters", tag)
	}
	return nil
}
