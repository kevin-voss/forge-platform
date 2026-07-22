// Package builder runs docker build with timeout and streamed logs.
package builder

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"forge.local/services/forge-build/internal/docker"
	"forge.local/services/forge-build/internal/logbuf"
)

// Options describe a single docker build invocation.
type Options struct {
	// ContextDir is the absolute build context directory (inside the workspace).
	ContextDir string
	// Dockerfile is the path to the Dockerfile relative to ContextDir,
	// or an absolute path inside the workspace that will be relativized.
	Dockerfile string
	// Tag is the temporary local image tag to apply.
	Tag string
	// Timeout bounds the build; zero means no additional timeout beyond ctx.
	Timeout time.Duration
}

// ImageBuilder builds container images.
type ImageBuilder interface {
	Build(ctx context.Context, opts Options, logs *logbuf.Buffer) error
}

// DockerBuilder uses the Docker Engine build API.
type DockerBuilder struct {
	engine *docker.Client
}

// New returns a Docker-backed image builder.
func New(engine *docker.Client) *DockerBuilder {
	return &DockerBuilder{engine: engine}
}

// Build runs docker build, streaming log lines into logs.
func (b *DockerBuilder) Build(ctx context.Context, opts Options, logs *logbuf.Buffer) error {
	if b == nil || b.engine == nil {
		return fmt.Errorf("docker builder is not configured")
	}
	if strings.TrimSpace(opts.ContextDir) == "" {
		return fmt.Errorf("build context is required")
	}
	if strings.TrimSpace(opts.Tag) == "" {
		return fmt.Errorf("image tag is required")
	}

	dockerfile := strings.TrimSpace(opts.Dockerfile)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if filepath.IsAbs(dockerfile) {
		rel, err := filepath.Rel(opts.ContextDir, dockerfile)
		if err != nil {
			return fmt.Errorf("resolve dockerfile relative to context: %w", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("dockerfile %q is outside build context %q", dockerfile, opts.ContextDir)
		}
		dockerfile = rel
	}

	buildCtx := ctx
	cancel := func() {}
	if opts.Timeout > 0 {
		buildCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()

	logs.Append(fmt.Sprintf("==> docker build -f %s -t %s %s", dockerfile, opts.Tag, opts.ContextDir))

	err := b.engine.BuildImage(buildCtx, docker.BuildOptions{
		ContextDir: opts.ContextDir,
		Dockerfile: dockerfile,
		Tags:       []string{opts.Tag},
		OnLine: func(line string) {
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				return
			}
			logs.Append(line)
		},
	})
	if err != nil {
		if buildCtx.Err() != nil && (ctx.Err() == nil) {
			logs.Append("==> build timed out")
			return fmt.Errorf("build timed out after %s: %w", opts.Timeout, err)
		}
		return err
	}
	logs.Append(fmt.Sprintf("==> built %s", opts.Tag))
	return nil
}

// LocalTag returns a temporary local tag for a build id.
func LocalTag(buildID string) string {
	id := strings.TrimSpace(buildID)
	return "forge-build-local:" + id
}
