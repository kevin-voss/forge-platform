// Package store provides durable JSON-file persistence for build records.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrorInfo is a structured build failure detail.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Record is the durable build snapshot persisted across restarts.
type Record struct {
	ID                 string     `json:"id"`
	Repo               string     `json:"repo"`
	Ref                string     `json:"ref"`
	ForgeYAML          string     `json:"forgeYaml,omitempty"`
	Project            string     `json:"project,omitempty"`
	Service            string     `json:"service,omitempty"`
	ServiceID          string     `json:"serviceId,omitempty"`
	EnvironmentID      string     `json:"environmentId,omitempty"`
	AutoDeploy         bool       `json:"autoDeploy,omitempty"`
	Commit             string     `json:"commit,omitempty"`
	Status             string     `json:"status"`
	Phase              string     `json:"phase"`
	Image              string     `json:"image,omitempty"`
	Digest             string     `json:"digest,omitempty"`
	ImageRecorded      bool       `json:"imageRecorded,omitempty"`
	RecordedImage      string     `json:"recordedImage,omitempty"`
	LinkedDeploymentID string     `json:"linkedDeploymentId,omitempty"`
	ControlError       string     `json:"controlError,omitempty"`
	StartedAt          time.Time  `json:"startedAt"`
	FinishedAt         *time.Time `json:"finishedAt,omitempty"`
	Error              *ErrorInfo `json:"error,omitempty"`
	WorkspacePath      string     `json:"workspacePath,omitempty"`
}

// Store is a directory of one JSON file per build id.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New creates a store rooted at dir, creating the directory if needed.
func New(dir string) (*Store, error) {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("store dir must be absolute, got %q", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create store dir %q: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("store dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("store dir %q is not a directory", dir)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the store root directory.
func (s *Store) Dir() string {
	return s.dir
}

// Put writes/updates a record atomically.
func (s *Store) Put(rec Record) error {
	if strings.TrimSpace(rec.ID) == "" {
		return fmt.Errorf("record id is required")
	}
	if err := sanitizeID(rec.ID); err != nil {
		return err
	}
	// Invariant: image/digest only on succeeded.
	if rec.Status != "succeeded" {
		rec.Image = ""
		rec.Digest = ""
	} else if rec.Image == "" {
		return fmt.Errorf("succeeded build %s must have image", rec.ID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal build %s: %w", rec.ID, err)
	}
	data = append(data, '\n')

	path := s.path(rec.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// Get loads a record by id.
func (s *Store) Get(id string) (Record, bool, error) {
	if err := sanitizeID(id); err != nil {
		return Record{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, false, nil
		}
		return Record{}, false, fmt.Errorf("read build %s: %w", id, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false, fmt.Errorf("decode build %s: %w", id, err)
	}
	return rec, true, nil
}

// List returns all persisted records (unordered).
func (s *Store) List() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("list store: %w", err)
	}
	out := make([]Record, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, ent.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", ent.Name(), err)
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("decode %s: %w", ent.Name(), err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// Delete removes a persisted record.
func (s *Store) Delete(id string) error {
	if err := sanitizeID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete build %s: %w", id, err)
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func sanitizeID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("build id is required")
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return fmt.Errorf("build id must not contain path separators or '..': %q", id)
	}
	if filepath.Base(id) != id {
		return fmt.Errorf("build id must be a single path segment: %q", id)
	}
	return nil
}
