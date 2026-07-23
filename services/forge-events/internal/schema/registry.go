package schema

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Mode controls publish behavior when a schema is missing or invalid.
type Mode string

const (
	ModeStrict Mode = "strict"
	ModeWarn   Mode = "warn"
)

// Metrics tracks schema load and rejection counters.
type Metrics struct {
	Loaded   atomic.Uint64
	Rejected atomic.Uint64
}

// Violation is a single JSON Schema validation failure (no payload values).
type Violation struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Keyword string `json:"keyword,omitempty"`
}

// Entry is a registered schema version for a subject.
type Entry struct {
	Subject       string
	SchemaVersion int
	SchemaJSON    json.RawMessage
	compiled      *jsonschema.Schema
}

// SubjectInfo is the listing view for GET /v1/schemas.
type SubjectInfo struct {
	Versions      []int `json:"versions"`
	LatestVersion int   `json:"latest_version"`
}

// SubjectDetail is the detail view for GET /v1/schemas/{subject}.
type SubjectDetail struct {
	Subject       string                     `json:"subject"`
	LatestVersion int                        `json:"latest_version"`
	Versions      map[string]json.RawMessage `json:"versions"`
}

// Registry loads and indexes event JSON Schemas by subject and version.
type Registry struct {
	mu      sync.RWMutex
	bySubj  map[string]map[int]*Entry
	mode    Mode
	log     *slog.Logger
	metrics *Metrics
	loadErr error
	loaded  bool
}

// NewRegistry constructs an empty registry.
func NewRegistry(mode Mode, log *slog.Logger, metrics *Metrics) *Registry {
	if mode != ModeWarn {
		mode = ModeStrict
	}
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &Registry{
		bySubj:  make(map[string]map[int]*Entry),
		mode:    mode,
		log:     log,
		metrics: metrics,
	}
}

// Mode returns the validation mode.
func (r *Registry) Mode() Mode { return r.mode }

// ReadyError reports whether schemas loaded successfully.
func (r *Registry) ReadyError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.loadErr != nil {
		return r.loadErr
	}
	if !r.loaded {
		return fmt.Errorf("event schemas not loaded")
	}
	return nil
}

// Load reads all *.schema.json files from dir, compiles them, and indexes them.
// On failure the registry is marked not-ready (fail fast for readiness).
func (r *Registry) Load(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		err := fmt.Errorf("schema dir is empty")
		r.setLoadError(err)
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		err = fmt.Errorf("read schema dir %q: %w", dir, err)
		r.setLoadError(err)
		return err
	}

	next := make(map[string]map[int]*Entry)
	var count int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".schema.json") {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			err = fmt.Errorf("read %s: %w", path, err)
			r.setLoadError(err)
			return err
		}
		entry, err := compileSchemaFile(path, raw)
		if err != nil {
			err = fmt.Errorf("compile %s: %w", path, err)
			r.setLoadError(err)
			return err
		}
		if next[entry.Subject] == nil {
			next[entry.Subject] = make(map[int]*Entry)
		}
		if _, exists := next[entry.Subject][entry.SchemaVersion]; exists {
			err = fmt.Errorf("duplicate schema %s v%d", entry.Subject, entry.SchemaVersion)
			r.setLoadError(err)
			return err
		}
		next[entry.Subject][entry.SchemaVersion] = entry
		count++
	}
	if count == 0 {
		err := fmt.Errorf("no *.schema.json files in %q", dir)
		r.setLoadError(err)
		return err
	}

	r.mu.Lock()
	r.bySubj = next
	r.loadErr = nil
	r.loaded = true
	r.mu.Unlock()
	r.metrics.Loaded.Store(uint64(count))
	r.log.Info("event schemas loaded",
		"span", "events.schema.load",
		"dir", dir,
		"count", count,
		"mode", string(r.mode),
	)
	return nil
}

func (r *Registry) setLoadError(err error) {
	r.mu.Lock()
	r.loadErr = err
	r.loaded = false
	r.bySubj = make(map[string]map[int]*Entry)
	r.mu.Unlock()
	r.metrics.Loaded.Store(0)
}

type fileMeta struct {
	Subject       string `json:"x-forge-subject"`
	SchemaVersion int    `json:"x-forge-schema-version"`
	Title         string `json:"title"`
}

func compileSchemaFile(path string, raw []byte) (*Entry, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	var meta fileMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	subject := strings.TrimSpace(meta.Subject)
	if subject == "" {
		subject = strings.TrimSpace(meta.Title)
	}
	if subject == "" {
		base := filepath.Base(path)
		subject = strings.TrimSuffix(base, ".schema.json")
		// Strip optional .vN suffix: application.crashed.v2 → application.crashed
		if i := strings.LastIndex(subject, ".v"); i > 0 {
			verPart := subject[i+2:]
			ok := true
			for _, r := range verPart {
				if r < '0' || r > '9' {
					ok = false
					break
				}
			}
			if ok && verPart != "" {
				subject = subject[:i]
			}
		}
	}
	if subject == "" {
		return nil, fmt.Errorf("missing x-forge-subject")
	}
	version := meta.SchemaVersion
	if version <= 0 {
		version = 1
	}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	url := "file://" + filepath.ToSlash(path)
	if err := compiler.AddResource(url, strings.NewReader(string(raw))); err != nil {
		return nil, fmt.Errorf("add resource: %w", err)
	}
	compiled, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	return &Entry{
		Subject:       subject,
		SchemaVersion: version,
		SchemaJSON:    append(json.RawMessage(nil), raw...),
		compiled:      compiled,
	}, nil
}

// Lookup returns the entry for subject at schemaVersion (0 = latest).
func (r *Registry) Lookup(subject string, schemaVersion int) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions, ok := r.bySubj[subject]
	if !ok || len(versions) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSchema, subject)
	}
	if schemaVersion <= 0 {
		latest := 0
		for v := range versions {
			if v > latest {
				latest = v
			}
		}
		schemaVersion = latest
	}
	entry, ok := versions[schemaVersion]
	if !ok {
		return nil, fmt.Errorf("%w: %s v%d", ErrUnknownSchemaVersion, subject, schemaVersion)
	}
	return entry, nil
}

// List returns subject → version info for GET /v1/schemas.
func (r *Registry) List() map[string]SubjectInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]SubjectInfo, len(r.bySubj))
	for subject, versions := range r.bySubj {
		vers := make([]int, 0, len(versions))
		latest := 0
		for v := range versions {
			vers = append(vers, v)
			if v > latest {
				latest = v
			}
		}
		sort.Ints(vers)
		out[subject] = SubjectInfo{Versions: vers, LatestVersion: latest}
	}
	return out
}

// Get returns detail for one subject.
func (r *Registry) Get(subject string) (SubjectDetail, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions, ok := r.bySubj[subject]
	if !ok || len(versions) == 0 {
		return SubjectDetail{}, fmt.Errorf("%w: %s", ErrUnknownSchema, subject)
	}
	detail := SubjectDetail{
		Subject:  subject,
		Versions: make(map[string]json.RawMessage, len(versions)),
	}
	for v, entry := range versions {
		if v > detail.LatestVersion {
			detail.LatestVersion = v
		}
		detail.Versions[fmt.Sprintf("%d", v)] = entry.SchemaJSON
	}
	return detail, nil
}

// Subjects returns sorted registered subjects (for tests).
func (r *Registry) Subjects() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.bySubj))
	for s := range r.bySubj {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
