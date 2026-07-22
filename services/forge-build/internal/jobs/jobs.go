// Package jobs implements the build job queue, worker pool, and durable records.
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
	"forge.local/services/forge-build/internal/store"
	"forge.local/services/forge-build/internal/workspace"
)

// Record is the in-memory build job state (durable fields synced to store).
type Record struct {
	ID                 string
	Repo               string
	Ref                string
	ForgeYAML          string
	Project            string
	Service            string
	ServiceID          string
	EnvironmentID      string
	AutoDeploy         bool
	Commit             string
	Status             Status
	Phase              Phase
	LocalImage         string
	Image              string
	Digest             string
	ImageRecorded      bool
	RecordedImage      string
	LinkedDeploymentID string
	ControlError       string
	StartedAt          time.Time
	FinishedAt         *time.Time
	Error              *BuildError
	Logs               *logbuf.Buffer
	WorkspaceDir       string

	cancelRequested bool
	runCancel       context.CancelFunc
}

// Request is an accepted build enqueue request (already validated).
type Request struct {
	Repo          string
	Ref           string
	ForgeYAML     string
	Project       string
	ServiceID     string
	EnvironmentID string
	AutoDeploy    bool
}

// Accepted is returned when a build is queued.
type Accepted struct {
	BuildID string
	Status  Status
}

// ListFilter selects builds for listing.
type ListFilter struct {
	Status  Status
	Service string
}

// CancelResult is returned from Cancel.
type CancelResult struct {
	Status string // "canceling"
}

// ErrNotFound is returned when a build id is unknown.
var ErrNotFound = fmt.Errorf("build not found")

// ErrConflict is returned when cancel is requested on a terminal build.
var ErrConflict = fmt.Errorf("build already terminal")

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
	Retention        time.Duration
	CleanupOnStart   bool

	ControlRetries      int
	ControlRetryBackoff time.Duration
	ControlTimeout      time.Duration
}

// Manager owns the queue, workers, and build records.
type Manager struct {
	cfg       Config
	ws        *workspace.Manager
	builder   builder.ImageBuilder
	publisher registry.Publisher
	control   ControlClient
	store     *store.Store
	log       *slog.Logger

	mu      sync.RWMutex
	records map[string]*Record

	queue   chan string
	workers int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a job manager. Call Recover then Start.
func New(cfg Config, ws *workspace.Manager, b builder.ImageBuilder, pub registry.Publisher, st *store.Store, log *slog.Logger) *Manager {
	return NewWithControl(cfg, ws, b, pub, nil, st, log)
}

// NewWithControl creates a job manager with an optional Control client.
func NewWithControl(cfg Config, ws *workspace.Manager, b builder.ImageBuilder, pub registry.Publisher, ctrl ControlClient, st *store.Store, log *slog.Logger) *Manager {
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
	if cfg.Retention <= 0 {
		cfg.Retention = 72 * time.Hour
	}
	if cfg.ControlRetries < 1 {
		cfg.ControlRetries = 5
	}
	if cfg.ControlRetryBackoff <= 0 {
		cfg.ControlRetryBackoff = 200 * time.Millisecond
	}
	if cfg.ControlTimeout <= 0 {
		cfg.ControlTimeout = 10 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	if st == nil {
		panic("jobs.New: store is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		cfg:       cfg,
		ws:        ws,
		builder:   b,
		publisher: pub,
		control:   ctrl,
		store:     st,
		log:       log,
		records:   make(map[string]*Record),
		queue:     make(chan string, 1024),
		workers:   cfg.MaxConcurrency,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Recover loads durable records, fails orphaned non-terminal builds, cleans
// workspaces, and applies retention. Call before Start.
func (m *Manager) Recover() error {
	persisted, err := m.store.List()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, p := range persisted {
		rec := recordFromStore(p, m.cfg.LogBufferLines)
		status := Status(p.Status)
		if !IsTerminal(status) {
			m.log.Warn("failing orphaned build on startup",
				"build_id", rec.ID,
				"prior_status", string(status),
				"prior_phase", string(rec.Phase),
			)
			rec.Status = StatusFailed
			rec.Phase = PhaseFailed
			rec.Image = ""
			rec.Digest = ""
			rec.FinishedAt = &now
			rec.Error = &BuildError{Code: ErrCodeInterrupted, Message: "build interrupted by service restart"}
			rec.Logs.Append("==> FAILED: interrupted by service restart")
			rec.Logs.Close()
			if err := m.persist(rec); err != nil {
				return err
			}
			m.cleanupWorkspace(rec)
		}
		m.mu.Lock()
		m.records[rec.ID] = rec
		m.mu.Unlock()
	}

	if m.cfg.CleanupOnStart {
		if err := m.sweepWorkspaces(); err != nil {
			m.log.Warn("startup workspace sweep incomplete", "error", err.Error())
		}
	}
	if err := m.applyRetention(now); err != nil {
		m.log.Warn("retention prune incomplete", "error", err.Error())
	}
	m.log.Info("build store recovered", "records", len(persisted))
	m.retryPendingControlLinks()
	return nil
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
		ID:            id,
		Repo:          strings.TrimSpace(req.Repo),
		Ref:           strings.TrimSpace(req.Ref),
		ForgeYAML:     forgeYAML,
		Project:       project,
		ServiceID:     strings.TrimSpace(req.ServiceID),
		EnvironmentID: strings.TrimSpace(req.EnvironmentID),
		AutoDeploy:    req.AutoDeploy,
		Status:        StatusQueued,
		Phase:         PhaseQueued,
		StartedAt:     time.Now().UTC(),
		Logs:          logbuf.New(m.cfg.LogBufferLines),
	}

	m.mu.Lock()
	m.records[id] = rec
	m.mu.Unlock()

	if err := m.persist(rec); err != nil {
		m.mu.Lock()
		delete(m.records, id)
		m.mu.Unlock()
		return Accepted{}, err
	}

	rec.Logs.Append(fmt.Sprintf("==> queued build %s", id))
	m.log.Info("build queued", "build_id", id, "repo", rec.Repo, "ref", rec.Ref)

	select {
	case m.queue <- id:
	case <-m.ctx.Done():
		_ = m.transition(rec, StatusFailed, PhaseFailed, &BuildError{
			Code:    ErrCodeShutdown,
			Message: "build service is shutting down",
		}, "", "")
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

// List returns builds matching the optional status/service filters.
func (m *Manager) List(filter ListFilter) []*Record {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Record, 0, len(m.records))
	wantStatus := Status(strings.TrimSpace(string(filter.Status)))
	wantService := strings.TrimSpace(filter.Service)
	for _, rec := range m.records {
		if wantStatus != "" && rec.Status != wantStatus {
			continue
		}
		if wantService != "" && rec.Service != wantService {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Cancel requests cancellation of a queued or running build.
func (m *Manager) Cancel(id string) (CancelResult, error) {
	m.mu.Lock()
	rec, ok := m.records[id]
	if !ok {
		m.mu.Unlock()
		return CancelResult{}, ErrNotFound
	}
	if IsTerminal(rec.Status) {
		m.mu.Unlock()
		return CancelResult{}, ErrConflict
	}
	rec.cancelRequested = true
	runCancel := rec.runCancel
	status := rec.Status
	m.mu.Unlock()

	if runCancel != nil {
		runCancel()
	}
	if status == StatusQueued {
		_ = m.transition(rec, StatusCanceled, PhaseCanceled, &BuildError{
			Code:    ErrCodeCanceled,
			Message: "build canceled",
		}, "", "")
		rec.Logs.Close()
		m.cleanupWorkspace(rec)
	}
	m.log.Info("build cancel requested", "build_id", id, "prior_status", string(status))
	return CancelResult{Status: "canceling"}, nil
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
			m.mu.RLock()
			canceled := rec.cancelRequested || rec.Status == StatusCanceled
			m.mu.RUnlock()
			if canceled {
				continue
			}
			m.runBuild(workerID, rec)
		}
	}
}

func (m *Manager) runBuild(workerID int, rec *Record) {
	runCtx, runCancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	if rec.cancelRequested || rec.Status == StatusCanceled {
		m.mu.Unlock()
		runCancel()
		return
	}
	rec.runCancel = runCancel
	m.mu.Unlock()
	defer func() {
		runCancel()
		m.mu.Lock()
		rec.runCancel = nil
		m.mu.Unlock()
	}()

	phaseStart := time.Now()
	if err := m.transition(rec, StatusRunning, PhaseCloning, nil, "", ""); err != nil {
		m.log.Error("unable to start build", "build_id", rec.ID, "error", err.Error())
		return
	}
	rec.Logs.Append(fmt.Sprintf("==> running on worker %d", workerID))
	m.log.Info("build phase", "build_id", rec.ID, "phase", string(PhaseCloning), "worker", workerID)

	start := time.Now()
	defer func() {
		rec.Logs.Close()
		m.log.Info("build finished",
			"build_id", rec.ID,
			"status", string(rec.Status),
			"phase", string(rec.Phase),
			"duration_ms", time.Since(start).Milliseconds(),
			"active_builds", m.ActiveCount(),
			"image", rec.Image,
			"digest", rec.Digest,
		)
		m.cleanupWorkspace(rec)
	}()

	dir, err := m.ws.Create(rec.ID)
	if err != nil {
		m.failOrCancel(rec, runCtx, ErrCodeWorkspace, "workspace create: "+err.Error())
		return
	}
	m.mu.Lock()
	rec.WorkspaceDir = dir
	m.mu.Unlock()
	_ = m.persist(rec)

	rec.Logs.Append(fmt.Sprintf("==> cloning %s @ %s", rec.Repo, rec.Ref))
	cloneCtx, cancelClone := context.WithTimeout(runCtx, m.cfg.BuildTimeout)
	result, err := git.CloneCheckout(cloneCtx, rec.Repo, rec.Ref, dir)
	cancelClone()
	if err != nil {
		if m.isCancel(runCtx, rec) {
			m.markCanceled(rec)
			return
		}
		m.failOrCancel(rec, runCtx, ErrCodeCloneFailed, "clone/checkout failed: "+err.Error())
		return
	}
	m.mu.Lock()
	rec.Commit = result.Commit
	m.mu.Unlock()
	rec.Logs.Append(fmt.Sprintf("==> checked out commit %s", rec.Commit))
	m.logPhase(rec, PhaseCloning, phaseStart)
	phaseStart = time.Now()

	yamlPath, err := manifest.ResolvePath(dir, rec.ForgeYAML)
	if err != nil {
		m.failOrCancel(rec, runCtx, ErrCodeManifestInvalid, "forge.yaml path: "+err.Error())
		return
	}
	mf, err := manifest.ParseFile(yamlPath)
	if err != nil {
		m.failOrCancel(rec, runCtx, ErrCodeManifestInvalid, "forge.yaml validation failed: "+err.Error())
		return
	}
	m.mu.Lock()
	rec.Service = mf.Service.Name
	m.mu.Unlock()
	rec.Logs.Append(fmt.Sprintf("==> forge.yaml ok (service=%s dockerfile=%s context=%s)",
		mf.Service.Name, mf.Build.Dockerfile, mf.Build.Context))

	contextDir, err := manifest.ResolvePath(dir, mf.Build.Context)
	if err != nil {
		m.failOrCancel(rec, runCtx, ErrCodeManifestInvalid, "build context: "+err.Error())
		return
	}
	dockerfilePath, err := manifest.ResolvePath(dir, mf.Build.Dockerfile)
	if err != nil {
		m.failOrCancel(rec, runCtx, ErrCodeManifestInvalid, "dockerfile: "+err.Error())
		return
	}

	if err := m.transition(rec, StatusRunning, PhaseBuilding, nil, "", ""); err != nil {
		m.failOrCancel(rec, runCtx, ErrCodeBuildFailed, err.Error())
		return
	}
	m.logPhase(rec, PhaseBuilding, phaseStart)
	phaseStart = time.Now()

	tag := builder.LocalTag(rec.ID)
	m.mu.Lock()
	rec.LocalImage = tag
	m.mu.Unlock()
	buildCtx, cancelBuild := context.WithTimeout(runCtx, m.cfg.BuildTimeout)
	err = m.builder.Build(buildCtx, builder.Options{
		ContextDir: contextDir,
		Dockerfile: dockerfilePath,
		Tag:        tag,
	}, rec.Logs)
	cancelBuild()
	if err != nil {
		if m.isCancel(runCtx, rec) {
			m.markCanceled(rec)
			return
		}
		if buildCtx.Err() == context.DeadlineExceeded {
			m.failOrCancel(rec, runCtx, ErrCodeBuildTimeout, fmt.Sprintf("build timed out after %s", m.cfg.BuildTimeout))
			return
		}
		m.failOrCancel(rec, runCtx, ErrCodeBuildFailed, "docker build failed: "+err.Error())
		return
	}
	m.logPhase(rec, PhaseBuilding, phaseStart)
	phaseStart = time.Now()

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
		m.failOrCancel(rec, runCtx, ErrCodeTagFailed, "image tag computation failed: "+err.Error())
		return
	}
	rec.Logs.Append(fmt.Sprintf("==> image refs versioned=%s latest=%s", refs.Versioned, refs.Latest))
	m.log.Info("image refs computed",
		"build_id", rec.ID,
		"versioned", refs.Versioned,
		"latest", refs.Latest,
	)

	if err := m.transition(rec, StatusRunning, PhasePushing, nil, "", ""); err != nil {
		m.failOrCancel(rec, runCtx, ErrCodePushFailed, err.Error())
		return
	}
	m.logPhase(rec, PhasePushing, phaseStart)

	pushCtx, cancelPush := context.WithTimeout(runCtx, m.cfg.BuildTimeout)
	digest, err := builder.PublishTags(pushCtx, m.publisher, tag, refs, rec.Logs)
	cancelPush()
	if err != nil {
		if m.isCancel(runCtx, rec) {
			m.markCanceled(rec)
			return
		}
		m.failOrCancel(rec, runCtx, ErrCodePushFailed, "registry push failed: "+err.Error())
		return
	}

	if m.isCancel(runCtx, rec) {
		m.markCanceled(rec)
		return
	}

	if err := m.transition(rec, StatusSucceeded, PhaseSucceeded, nil, refs.Versioned, digest); err != nil {
		m.failOrCancel(rec, runCtx, ErrCodePushFailed, err.Error())
		return
	}
	rec.Logs.Append("==> build succeeded")
	// Control integration runs after success; failures never downgrade build status
	// or remove the pushed image. Use manager context so a canceled runCtx cannot
	// skip recording after the image is already pushed.
	m.recordWithControl(m.ctx, rec)
}

func (m *Manager) logPhase(rec *Record, phase Phase, started time.Time) {
	m.log.Info("build phase complete",
		"build_id", rec.ID,
		"phase", string(phase),
		"duration_ms", time.Since(started).Milliseconds(),
	)
}

func (m *Manager) failOrCancel(rec *Record, runCtx context.Context, code, message string) {
	if m.isCancel(runCtx, rec) {
		m.markCanceled(rec)
		return
	}
	_ = m.transition(rec, StatusFailed, PhaseFailed, &BuildError{
		Code:    code,
		Message: sanitizeError(message),
	}, "", "")
	rec.Logs.Append("==> FAILED: " + sanitizeError(message))
	m.log.Warn("build failed", "build_id", rec.ID, "code", code, "error", sanitizeError(message))
}

func (m *Manager) markCanceled(rec *Record) {
	m.mu.RLock()
	terminal := IsTerminal(rec.Status)
	m.mu.RUnlock()
	if terminal {
		return
	}
	_ = m.transition(rec, StatusCanceled, PhaseCanceled, &BuildError{
		Code:    ErrCodeCanceled,
		Message: "build canceled",
	}, "", "")
	rec.Logs.Append("==> CANCELED")
	m.log.Info("build canceled", "build_id", rec.ID)
}

func (m *Manager) isCancel(runCtx context.Context, rec *Record) bool {
	m.mu.RLock()
	requested := rec.cancelRequested
	m.mu.RUnlock()
	if requested {
		return true
	}
	return runCtx.Err() == context.Canceled
}

func (m *Manager) transition(rec *Record, status Status, phase Phase, buildErr *BuildError, image, digest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ValidateTransition(rec.Status, rec.Phase, status, phase); err != nil {
		return err
	}
	if !ImageInvariantOK(status, image, digest) {
		return fmt.Errorf("image invariant violated: status=%s image=%q digest=%q", status, image, digest)
	}

	prevStatus, prevPhase := rec.Status, rec.Phase
	rec.Status = status
	rec.Phase = phase
	if IsTerminal(status) {
		now := time.Now().UTC()
		rec.FinishedAt = &now
		rec.LocalImage = ""
		if status == StatusSucceeded {
			rec.Image = image
			rec.Digest = digest
			rec.Error = nil
		} else {
			rec.Image = ""
			rec.Digest = ""
			rec.Error = buildErr
		}
	} else {
		rec.Image = ""
		rec.Digest = ""
		if buildErr != nil {
			rec.Error = buildErr
		}
	}

	if err := m.persistLocked(rec); err != nil {
		rec.Status = prevStatus
		rec.Phase = prevPhase
		return err
	}
	m.log.Info("build transition",
		"build_id", rec.ID,
		"from", string(prevStatus)+"/"+string(prevPhase),
		"to", string(status)+"/"+string(phase),
	)
	return nil
}

func (m *Manager) persist(rec *Record) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.persistLocked(rec)
}

func (m *Manager) persistLocked(rec *Record) error {
	return m.store.Put(toStoreRecord(rec))
}

func (m *Manager) cleanupWorkspace(rec *Record) {
	id := rec.ID
	if err := m.ws.Cleanup(id); err != nil {
		m.log.Warn("workspace cleanup failed", "build_id", id, "error", err.Error())
		rec.Logs.Append("==> workspace cleanup failed: " + err.Error())
	} else {
		rec.Logs.Append("==> workspace cleaned")
		m.log.Info("workspace cleaned", "build_id", id)
	}
	m.mu.Lock()
	rec.WorkspaceDir = ""
	m.mu.Unlock()
	_ = m.persist(rec)
}

func (m *Manager) sweepWorkspaces() error {
	entries, err := m.ws.Entries()
	if err != nil {
		return err
	}
	for _, id := range entries {
		m.log.Info("startup sweep removing workspace", "build_id", id)
		if err := m.ws.Cleanup(id); err != nil {
			m.log.Warn("startup sweep cleanup failed", "build_id", id, "error", err.Error())
		}
	}
	return nil
}

func (m *Manager) applyRetention(now time.Time) error {
	cutoff := now.Add(-m.cfg.Retention)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rec := range m.records {
		if !IsTerminal(rec.Status) || rec.FinishedAt == nil {
			continue
		}
		if rec.FinishedAt.After(cutoff) {
			continue
		}
		if err := m.store.Delete(id); err != nil {
			return err
		}
		delete(m.records, id)
		m.log.Info("retention pruned build", "build_id", id, "finished_at", rec.FinishedAt.Format(time.RFC3339))
	}
	return nil
}

func toStoreRecord(rec *Record) store.Record {
	out := store.Record{
		ID:                 rec.ID,
		Repo:               rec.Repo,
		Ref:                rec.Ref,
		ForgeYAML:          rec.ForgeYAML,
		Project:            rec.Project,
		Service:            rec.Service,
		ServiceID:          rec.ServiceID,
		EnvironmentID:      rec.EnvironmentID,
		AutoDeploy:         rec.AutoDeploy,
		Commit:             rec.Commit,
		Status:             string(rec.Status),
		Phase:              string(rec.Phase),
		Image:              rec.Image,
		Digest:             rec.Digest,
		ImageRecorded:      rec.ImageRecorded,
		RecordedImage:      rec.RecordedImage,
		LinkedDeploymentID: rec.LinkedDeploymentID,
		ControlError:       rec.ControlError,
		StartedAt:          rec.StartedAt,
		FinishedAt:         rec.FinishedAt,
		WorkspacePath:      rec.WorkspaceDir,
	}
	if rec.Error != nil {
		out.Error = &store.ErrorInfo{Code: rec.Error.Code, Message: rec.Error.Message}
	}
	if out.Status != "succeeded" {
		out.Image = ""
		out.Digest = ""
		out.ImageRecorded = false
		out.RecordedImage = ""
		out.LinkedDeploymentID = ""
	}
	return out
}

func recordFromStore(p store.Record, logLines int) *Record {
	logs := logbuf.New(logLines)
	logs.Close()
	rec := &Record{
		ID:                 p.ID,
		Repo:               p.Repo,
		Ref:                p.Ref,
		ForgeYAML:          p.ForgeYAML,
		Project:            p.Project,
		Service:            p.Service,
		ServiceID:          p.ServiceID,
		EnvironmentID:      p.EnvironmentID,
		AutoDeploy:         p.AutoDeploy,
		Commit:             p.Commit,
		Status:             Status(p.Status),
		Phase:              Phase(p.Phase),
		Image:              p.Image,
		Digest:             p.Digest,
		ImageRecorded:      p.ImageRecorded,
		RecordedImage:      p.RecordedImage,
		LinkedDeploymentID: p.LinkedDeploymentID,
		ControlError:       p.ControlError,
		StartedAt:          p.StartedAt,
		FinishedAt:         p.FinishedAt,
		Logs:               logs,
		WorkspaceDir:       p.WorkspacePath,
	}
	if p.Error != nil {
		rec.Error = &BuildError{Code: p.Error.Code, Message: p.Error.Message}
	}
	if rec.Status != StatusSucceeded {
		rec.Image = ""
		rec.Digest = ""
		rec.ImageRecorded = false
		rec.RecordedImage = ""
		rec.LinkedDeploymentID = ""
	}
	return rec
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
