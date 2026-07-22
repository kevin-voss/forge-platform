// Package git clones and checks out repositories into build workspaces.
package git

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result is a successful clone/checkout into Dest.
type Result struct {
	Dest   string
	Commit string
}

// ValidateRepo accepts only local filesystem sources (absolute path or file://).
func ValidateRepo(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", fmt.Errorf("repo is required")
	}
	if strings.HasPrefix(repo, "file://") {
		u, err := url.Parse(repo)
		if err != nil {
			return "", fmt.Errorf("invalid file:// repo URL: %w", err)
		}
		path := u.Path
		if path == "" {
			path = u.Opaque
		}
		// file://localhost/path or file:///path
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("file:// repo host %q is not allowed", u.Host)
		}
		if path == "" {
			return "", fmt.Errorf("file:// repo path is empty")
		}
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("file:// repo path must be absolute, got %q", path)
		}
		return filepath.Clean(path), nil
	}
	lower := strings.ToLower(repo)
	if strings.Contains(lower, "://") {
		return "", fmt.Errorf("remote git URLs are not allowed; use a local path or file:// URL")
	}
	if !filepath.IsAbs(repo) {
		return "", fmt.Errorf("repo path must be absolute or file://, got %q", repo)
	}
	return filepath.Clean(repo), nil
}

// CloneCheckout clones repo into destDir and checks out ref (branch/tag/commit).
// destDir must be empty or not exist; it becomes the working tree root.
func CloneCheckout(ctx context.Context, repo, ref, destDir string) (Result, error) {
	localPath, err := ValidateRepo(repo)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return Result{}, fmt.Errorf("repo source %q: %w", localPath, err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("repo source %q is not a directory", localPath)
	}

	destDir = filepath.Clean(destDir)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create clone destination: %w", err)
	}

	// Clone into destDir (must be empty). Avoid --local hardlinks: fixture mounts
	// and the workspace volume are often on different devices inside Compose.
	if err := runGit(ctx, "", "clone", "--no-hardlinks", "--no-checkout", localPath, destDir); err != nil {
		return Result{}, fmt.Errorf("git clone: %w", err)
	}
	if err := runGit(ctx, destDir, "checkout", "--force", ref); err != nil {
		return Result{}, fmt.Errorf("git checkout %q: %w", ref, err)
	}
	commit, err := runGitOutput(ctx, destDir, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return Result{}, fmt.Errorf("resolved empty commit for ref %q", ref)
	}
	return Result{Dest: destDir, Commit: commit}, nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	_, err := runGitOutput(ctx, dir, args...)
	return err
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}
