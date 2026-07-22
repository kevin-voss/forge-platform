package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manager creates and cleans isolated per-build directories under a root volume.
type Manager struct {
	root string
}

// New creates a workspace manager rooted at root.
// The root directory must already exist and be writable.
func New(root string) (*Manager, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("workspace root must be absolute, got %q", root)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("workspace root %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", root)
	}
	probe := filepath.Join(root, ".forge-build-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return nil, fmt.Errorf("workspace root %q is not writable: %w", root, err)
	}
	_ = os.Remove(probe)
	return &Manager{root: root}, nil
}

// Root returns the workspace root directory.
func (m *Manager) Root() string {
	return m.root
}

// Create makes an isolated directory for buildID with restrictive permissions (0700).
func (m *Manager) Create(buildID string) (string, error) {
	id, err := sanitizeBuildID(buildID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(m.root, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("create workspace %q: %w", dir, err)
	}
	return dir, nil
}

// Cleanup removes the isolated directory for buildID and all contents.
func (m *Manager) Cleanup(buildID string) error {
	id, err := sanitizeBuildID(buildID)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.root, id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("cleanup workspace %q: %w", dir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		if err == nil {
			return fmt.Errorf("cleanup workspace %q: directory still exists", dir)
		}
		return fmt.Errorf("cleanup workspace %q: %w", dir, err)
	}
	return nil
}

// Entries returns immediate subdirectory names under the workspace root
// (typically build ids). Non-directories and hidden names are skipped.
func (m *Manager) Entries() ([]string, error) {
	ents, err := os.ReadDir(m.root)
	if err != nil {
		return nil, fmt.Errorf("list workspace root %q: %w", m.root, err)
	}
	out := make([]string, 0, len(ents))
	for _, ent := range ents {
		name := ent.Name()
		if !ent.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func sanitizeBuildID(buildID string) (string, error) {
	id := strings.TrimSpace(buildID)
	if id == "" {
		return "", fmt.Errorf("build id is required")
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("build id must not contain path separators or '..': %q", buildID)
	}
	if filepath.Base(id) != id {
		return "", fmt.Errorf("build id must be a single path segment: %q", buildID)
	}
	return id, nil
}
