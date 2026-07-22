// Package jobs implements the build job queue, worker pool, and in-memory records.
package jobs

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"forge.local/services/forge-build/internal/builder"
	"forge.local/services/forge-build/internal/git"
	"forge.local/services/forge-build/internal/logbuf"
	"forge.local/services/forge-build/internal/manifest"
	"forge.local/services/forge-build/internal/registry"
	"forge.local/services/forge-build/internal/workspace"
)

// Status is the lifecycle state of a build job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Record is the in-memory build job state.
type Record struct {
	ID           string
	Repo         string
	Ref          string
	ForgeYAML    string
	Project      string
	Commit       string
	Status       Status
	LocalImage   string
	Image        string
	Digest       string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Error        string
	Logs         *logbuf.Buffer
	WorkspaceDir string
}

// Request is an accepted build enqueue request (already validated).
type Request struct {
	Repo      string
	Ref       string
	ForgeYAML string
	Project   string
}

// Accepted is returned when a build is queued.
type Accepted struct {
	BuildID string
	Status  Status
}

// Config configures the job manager.
type Config struct {
	MaxConcurrency   int
	BuildTimeout     time.Duration
	LogBufferLines   int
	DefaultForgeYAML string
	Registry         string
	ImageNamePattern string
	DefaultProject   string
	PushLatest       bool
}

// Manager owns the queue, workers, and build records.
type Manager struct {
	cfg       Config
	ws        *workspace.Manager
	builder   builder.ImageBuilder
	publisher registry.Publisher
	log       *slog.Logger

	mu      sync.RWMutex
	records map[string]*Record

	queue   chan string
	workers int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a job manager. Call Start to begin workers.
// publisher may be a stub in unit tests; production uses registry.Client.
func New(cfg Config, ws *workspace.Manager, b builder.ImageBuilder, pub registry.Publisher, log *slog.Logger) *Manager {
	if cfg.MaxConcurrency < 1 {
		cfg.MaxConcurrency = 1
	}
	if cfg.LogBufferLines < 1 {
		cfg.LogBufferLines = 5000
	}
	if strings.TrimSpace(cfg.DefaultForgeYAML) == "" {
		cfg.DefaultForgeYAML = manifest.DefaultPath
	}
	if strings.TrimSpace(cfg.Registry) == "" {
		cfg.Registry = registry.DefaultRegistry
	}
	if strings.TrimSpace(cfg.ImageNamePattern) == "" {
		cfg.ImageNamePattern = registry.DefaultImageNamePattern
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:       cfg,
		ws:        ws,
		builder:   b,
		publisher: pub,
		log:       log,
		records:   make(map[string]*Record),
		queue:     make(chan string, 1024),
		workers:   cfg.MaxConcurrency,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start launches the bounded worker pool.
func (m *Manager) Start() {
	for i := 0; i < m.workers; i++ {
		m.wg.Add(1)
		go func(workerID int) {
			defer m.wg.Done()
			m.workerLoop(workerID)
		}(i)
	}
	m.log.Info("build workers started", "concurrency", m.workers)
}

// Stop cancels workers and waits for them to exit.
func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
}

// Enqueue creates a queued record and schedules it on the worker pool.
func (m *Manager) Enqueue(req Request) (Accepted, error) {
	if strings.TrimSpace(req.Repo) == "" {
		return Accepted{}, &manifest.ValidationError{Field: "repo", Message: "repo is required"}
	}
	if strings.TrimSpace(req.Ref) == "" {
		return Accepted{}, &manifest.ValidationError{Field: "ref", Message: "ref is required"}
	}
	if _, err := git.ValidateRepo(req.Repo); err != nil {
		return Accepted{}, &manifest.ValidationError{Field: "repo", Message: err.Error()}
	}
	forgeYAML := strings.TrimSpace(req.ForgeYAML)
	if forgeYAML == "" {
		forgeYAML = m.cfg.DefaultForgeYAML
	}
	if err := manifest.ValidateRepoRelativePath("forgeYamlPath", forgeYAML); err != nil {
		return Accepted{}, err
	}

	project := strings.TrimSpace(req.Project)
	if project == "" {
		project = m.cfg.DefaultProject
	}

	id := newBuildID()
	rec := &Record{
		ID:        id,
		Repo:      strings.TrimSpace(req.Repo),
		Ref:       strings.TrimSpace(req.Ref),
		ForgeYAML: forgeYAML,
		Project:   project,
		Status:    StatusQueued,
		StartedAt: time.Now().UTC(),
		Logs:      logbuf.New(m.cfg.LogBufferLines),
	}

	m.mu.Lock()
	m.records[id] = rec
	m.mu.Unlock()

	rec.Logs.Append(fmt.Sprintf("==> queued build %s", id))
	m.log.Info("build queued", "build_id", id, "repo", rec.Repo, "ref", rec.Ref)

	select {
	case m.queue <- id:
	case <-m.ctx.Done():
		m.fail(rec, "build service is shutting down")
		return Accepted{}, fmt.Errorf("build service is shutting down")
	}

	return Accepted{BuildID: id, Status: StatusQueued}, nil
}

// Get returns a build record by id.
func (m *Manager) Get(id string) (*Record, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.records[id]
	return rec, ok
}

// ActiveCount returns how many builds are currently running.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, rec := range m.records {
		if rec.Status == StatusRunning {
			n++
		}
	}
	return n
}

func (m *Manager) workerLoop(workerID int) {
	for {
		select {
		case <-m.ctx.Done():
			return
		case id, ok := <-m.queue:
			if !ok {
				return
			}
			rec, found := m.Get(id)
			if !found {
				continue
			}
			m.runBuild(workerID, rec)
		}
	}
}

func (m *Manager) runBuild(workerID int, rec *Record) {
	m.setStatus(rec, StatusRunning)
	rec.Logs.Append(fmt.Sprintf("==> running on worker %d", workerID))
	m.log.Info("build running", "build_id", rec.ID, "worker", workerID)

	start := time.Now()
	defer func() {
		rec.Logs.Close()
		m.log.Info("build finished",
			"build_id", rec.ID,
			"status", string(rec.Status),
			"duration_ms", time.Since(start).Milliseconds(),
			"active_builds", m.ActiveCount(),
			"image", rec.Image,
			"digest", rec.Digest,
		)
	}()

	dir, err := m.ws.Create(rec.ID)
	if err != nil {
		m.fail(rec, "workspace create: "+err.Error())
		return
	}
	rec.WorkspaceDir = dir
	defer func() {
		if err := m.ws.Cleanup(rec.ID); err != nil {
			m.log.Warn("workspace cleanup failed", "build_id", rec.ID, "error", err.Error())
			rec.Logs.Append("==> workspace cleanup failed: " + err.Error())
		} else {
			rec.Logs.Append("==> workspace cleaned")
		}
		rec.WorkspaceDir = ""
	}()

	rec.Logs.Append(fmt.Sprintf("==> cloning %s @ %s", rec.Repo, rec.Ref))
	cloneCtx, cancelClone := context.WithTimeout(m.ctx, m.cfg.BuildTimeout)
	result, err := git.CloneCheckout(cloneCtx, rec.Repo, rec.Ref, dir)
	cancelClone()
	if err != nil {
		m.fail(rec, "clone/checkout failed: "+err.Error())
		return
	}
	rec.Commit = result.Commit
	rec.Logs.Append(fmt.Sprintf("==> checked out commit %s", rec.Commit))
	m.log.Info("build checked out", "build_id", rec.ID, "commit", rec.Commit)

	yamlPath, err := manifest.ResolvePath(dir, rec.ForgeYAML)
	if err != nil {
		m.fail(rec, "forge.yaml path: "+err.Error())
		return
	}
	mf, err := manifest.ParseFile(yamlPath)
	if err != nil {
		m.fail(rec, "forge.yaml validation failed: "+err.Error())
		return
	}
	rec.Logs.Append(fmt.Sprintf("==> forge.yaml ok (service=%s dockerfile=%s context=%s)",
		mf.Service.Name, mf.Build.Dockerfile, mf.Build.Context))

	contextDir, err := manifest.ResolvePath(dir, mf.Build.Context)
	if err != nil {
		m.fail(rec, "build context: "+err.Error())
		return
	}
	dockerfilePath, err := manifest.ResolvePath(dir, mf.Build.Dockerfile)
	if err != nil {
		m.fail(rec, "dockerfile: "+err.Error())
		return
	}

	tag := builder.LocalTag(rec.ID)
	rec.LocalImage = tag
	buildCtx, cancelBuild := context.WithTimeout(m.ctx, m.cfg.BuildTimeout)
	err = m.builder.Build(buildCtx, builder.Options{
		ContextDir: contextDir,
		Dockerfile: dockerfilePath,
		Tag:        tag,
	}, rec.Logs)
	cancelBuild()
	if err != nil {
		if buildCtx.Err() == context.DeadlineExceeded {
			m.fail(rec, fmt.Sprintf("build timed out after %s", m.cfg.BuildTimeout))
			return
		}
		m.fail(rec, "docker build failed: "+err.Error())
		return
	}

	refs, err := registry.ComputeRefs(registry.TagInput{
		Registry:   m.cfg.Registry,
		Pattern:    m.cfg.ImageNamePattern,
		Project:    rec.Project,
		Service:    mf.Service.Name,
		Commit:     rec.Commit,
		BuildID:    rec.ID,
		PushLatest: m.cfg.PushLatest,
	})
	if err != nil {
		m.fail(rec, "image tag computation failed: "+err.Error())
		return
	}
	rec.Logs.Append(fmt.Sprintf("==> image refs versioned=%s latest=%s", refs.Versioned, refs.Latest))
	m.log.Info("image refs computed",
		"build_id", rec.ID,
		"versioned", refs.Versioned,
		"latest", refs.Latest,
	)

	pushCtx, cancelPush := context.WithTimeout(m.ctx, m.cfg.BuildTimeout)
	digest, err := builder.PublishTags(pushCtx, m.publisher, tag, refs, rec.Logs)
	cancelPush()
	if err != nil {
		m.fail(rec, "registry push failed: "+err.Error())
		return
	}

	now := time.Now().UTC()
	m.mu.Lock()
	rec.Status = StatusSucceeded
	rec.Image = refs.Versioned
	rec.Digest = digest
	rec.FinishedAt = &now
	rec.Error = ""
	m.mu.Unlock()
	rec.Logs.Append("==> build succeeded")
}

func (m *Manager) setStatus(rec *Record, status Status) {
	m.mu.Lock()
	rec.Status = status
	m.mu.Unlock()
}

func (m *Manager) fail(rec *Record, message string) {
	message = sanitizeError(message)
	now := time.Now().UTC()
	m.mu.Lock()
	rec.Status = StatusFailed
	rec.FinishedAt = &now
	rec.Error = message
	rec.LocalImage = ""
	rec.Image = ""
	rec.Digest = ""
	m.mu.Unlock()
	rec.Logs.Append("==> FAILED: " + message)
	m.log.Warn("build failed", "build_id", rec.ID, "error", message)
}

func sanitizeError(message string) string {
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	message = strings.ReplaceAll(message, "\n", " | ")
	for strings.Contains(message, " |  | ") {
		message = strings.ReplaceAll(message, " |  | ", " | ")
	}
	return strings.TrimSpace(message)
}

func newBuildID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
