package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/builder"
	"forge.local/services/forge-build/internal/jobs"
	"forge.local/services/forge-build/internal/logbuf"
	"forge.local/services/forge-build/internal/registry"
	"forge.local/services/forge-build/internal/store"
	"forge.local/services/forge-build/internal/workspace"
)

func stubPublisher() registry.Publisher {
	return &registry.StubPublisher{Digest: "sha256:stub"}
}

type fakeBuilder struct {
	started   atomic.Int32
	maxActive atomic.Int32
	active    atomic.Int32
	delay     time.Duration
	err       error
	startedCh chan struct{}
}

func (f *fakeBuilder) Build(ctx context.Context, opts builder.Options, logs *logbuf.Buffer) error {
	f.started.Add(1)
	if f.startedCh != nil {
		select {
		case f.startedCh <- struct{}{}:
		default:
		}
	}
	cur := f.active.Add(1)
	for {
		prev := f.maxActive.Load()
		if cur <= prev || f.maxActive.CompareAndSwap(prev, cur) {
			break
		}
	}
	defer f.active.Add(-1)

	logs.Append("fake-build-start " + opts.Tag)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		logs.Append("fake-build-error")
		return f.err
	}
	logs.Append("fake-build-done")
	return nil
}

func newMgr(t *testing.T, wsRoot string, b builder.ImageBuilder, pub registry.Publisher, cfg jobs.Config) *jobs.Manager {
	t.Helper()
	ws, err := workspace.New(wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrency < 1 {
		cfg.MaxConcurrency = 1
	}
	if cfg.BuildTimeout == 0 {
		cfg.BuildTimeout = 10 * time.Second
	}
	if cfg.LogBufferLines == 0 {
		cfg.LogBufferLines = 100
	}
	if cfg.DefaultForgeYAML == "" {
		cfg.DefaultForgeYAML = "forge.yaml"
	}
	mgr := jobs.New(cfg, ws, b, pub, st, slog.Default())
	if err := mgr.Recover(); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestQueueRespectsConcurrencyLimit(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	fb := &fakeBuilder{delay: 300 * time.Millisecond}
	mgr := newMgr(t, wsRoot, fb, stubPublisher(), jobs.Config{
		MaxConcurrency: 2,
		BuildTimeout:   10 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	var ids []string
	for i := 0; i < 4; i++ {
		acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main", ForgeYAML: "forge.yaml"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, acc.BuildID)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fb.maxActive.Load() > 2 {
			t.Fatalf("max active = %d, want <= 2", fb.maxActive.Load())
		}
		if fb.started.Load() == 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fb.maxActive.Load() > 2 {
		t.Fatalf("max active = %d", fb.maxActive.Load())
	}

	waitSucceeded(t, mgr, ids, 10*time.Second)
	if fb.started.Load() != 4 {
		t.Fatalf("started = %d", fb.started.Load())
	}
}

func TestTimeoutCancelsLongBuild(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	fb := &fakeBuilder{delay: 5 * time.Second}
	mgr := newMgr(t, wsRoot, fb, stubPublisher(), jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   200 * time.Millisecond,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTerminal(t, mgr, acc.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusFailed {
		t.Fatalf("status = %s, want failed", rec.Status)
	}
	if rec.Error == nil || !strings.Contains(rec.Error.Message, "timed out") {
		t.Fatalf("error = %+v", rec.Error)
	}
	if rec.Error.Code != jobs.ErrCodeBuildTimeout {
		t.Fatalf("code = %q", rec.Error.Code)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
}

func TestBadRefFailsAndCleansWorkspace(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	mgr := newMgr(t, wsRoot, &fakeBuilder{}, stubPublisher(), jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   10 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	acc, err := mgr.Enqueue(jobs.Request{Repo: "file://" + repo, Ref: "missing-branch"})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTerminal(t, mgr, acc.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusFailed {
		t.Fatalf("status = %s", rec.Status)
	}
	if rec.Error == nil || !strings.Contains(rec.Error.Message, "clone/checkout") {
		t.Fatalf("error = %+v", rec.Error)
	}
	if rec.Image != "" {
		t.Fatalf("image = %q", rec.Image)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
	logs := strings.Join(rec.Logs.Snapshot(), "\n")
	if !strings.Contains(logs, "FAILED") {
		t.Fatalf("logs missing failure: %s", logs)
	}
}

func TestSuccessfulBuildRecordsPushedImage(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	pub := &registry.StubPublisher{Digest: "sha256:unit-test"}
	mgr := newMgr(t, wsRoot, &fakeBuilder{}, pub, jobs.Config{
		MaxConcurrency:   1,
		BuildTimeout:     10 * time.Second,
		Registry:         "localhost:5000",
		ImageNamePattern: "{project}-{service}",
		PushLatest:       true,
	})
	mgr.Start()
	defer mgr.Stop()

	acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main", Project: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTerminal(t, mgr, acc.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusSucceeded || rec.Phase != jobs.PhaseSucceeded {
		t.Fatalf("status=%s phase=%s error=%+v", rec.Status, rec.Phase, rec.Error)
	}
	if pub.Calls != 1 {
		t.Fatalf("publisher calls = %d", pub.Calls)
	}
	if rec.Digest != "sha256:unit-test" {
		t.Fatalf("digest = %q", rec.Digest)
	}
	if !strings.HasPrefix(rec.Image, "localhost:5000/acme-api:") {
		t.Fatalf("image = %q", rec.Image)
	}
	if !jobs.ImageInvariantOK(rec.Status, rec.Image, rec.Digest) {
		t.Fatal("image invariant violated")
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
}

func TestPushFailureLeavesNoImage(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	pub := &registry.StubPublisher{Err: errors.New("registry unreachable")}
	mgr := newMgr(t, wsRoot, &fakeBuilder{}, pub, jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   10 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main", Project: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTerminal(t, mgr, acc.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusFailed {
		t.Fatalf("status=%s", rec.Status)
	}
	if rec.Image != "" || rec.Digest != "" {
		t.Fatalf("image=%q digest=%q", rec.Image, rec.Digest)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
}

func TestCancelQueuedBuild(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	// Block the single worker with a long build so the second stays queued.
	fb := &fakeBuilder{delay: 2 * time.Second}
	mgr := newMgr(t, wsRoot, fb, stubPublisher(), jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   10 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	first, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Wait until first is running so second is still queued.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, _ := mgr.Get(first.BuildID)
		if rec.Status == jobs.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first never running: %s", rec.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	res, err := mgr.Cancel(second.BuildID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "canceling" {
		t.Fatalf("cancel result=%+v", res)
	}
	rec := waitTerminal(t, mgr, second.BuildID, 3*time.Second)
	if rec.Status != jobs.StatusCanceled {
		t.Fatalf("status=%s", rec.Status)
	}
	if rec.Image != "" {
		t.Fatalf("image=%q", rec.Image)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, second.BuildID), 3*time.Second)
	_ = waitTerminal(t, mgr, first.BuildID, 5*time.Second)
}

func TestCancelRunningBuild(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	started := make(chan struct{}, 1)
	fb := &fakeBuilder{delay: 5 * time.Second, startedCh: started}
	mgr := newMgr(t, wsRoot, fb, stubPublisher(), jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   10 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("build never started")
	}
	if _, err := mgr.Cancel(acc.BuildID); err != nil {
		t.Fatal(err)
	}
	rec := waitTerminal(t, mgr, acc.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusCanceled {
		t.Fatalf("status=%s error=%+v", rec.Status, rec.Error)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
}

func TestCancelTerminalReturnsConflict(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	mgr := newMgr(t, wsRoot, &fakeBuilder{}, stubPublisher(), jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   5 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()
	defer mgr.Stop()

	acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitTerminal(t, mgr, acc.BuildID, 5*time.Second)
	_, err = mgr.Cancel(acc.BuildID)
	if !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecoverFailsOrphanRunningAndCleansWorkspace(t *testing.T) {
	wsRoot := t.TempDir()
	storeDir := t.TempDir()
	st, err := store.New(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	id := "33333333-3333-4333-8333-333333333333"
	dir, err := ws.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leftover.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := st.Put(store.Record{
		ID:            id,
		Repo:          "file:///tmp/x",
		Ref:           "main",
		Status:        "running",
		Phase:         "building",
		StartedAt:     now,
		WorkspacePath: dir,
	}); err != nil {
		t.Fatal(err)
	}

	mgr := jobs.New(jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   time.Second,
		CleanupOnStart: true,
		Retention:      72 * time.Hour,
	}, ws, &fakeBuilder{}, stubPublisher(), st, slog.Default())
	if err := mgr.Recover(); err != nil {
		t.Fatal(err)
	}
	rec, ok := mgr.Get(id)
	if !ok {
		t.Fatal("missing recovered record")
	}
	if rec.Status != jobs.StatusFailed || rec.Phase != jobs.PhaseFailed {
		t.Fatalf("status=%s phase=%s", rec.Status, rec.Phase)
	}
	if rec.Error == nil || rec.Error.Code != jobs.ErrCodeInterrupted {
		t.Fatalf("error=%+v", rec.Error)
	}
	if rec.Image != "" {
		t.Fatalf("image=%q", rec.Image)
	}
	waitWorkspaceGone(t, dir, 2*time.Second)

	// Status survives in durable store for a fresh manager.
	st2, err := store.New(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	ws2, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr2 := jobs.New(jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   time.Second,
		CleanupOnStart: true,
	}, ws2, &fakeBuilder{}, stubPublisher(), st2, slog.Default())
	if err := mgr2.Recover(); err != nil {
		t.Fatal(err)
	}
	rec2, ok := mgr2.Get(id)
	if !ok || rec2.Status != jobs.StatusFailed {
		t.Fatalf("durable status missing: ok=%v rec=%+v", ok, rec2)
	}
}

func TestRejectRemoteRepoOnEnqueue(t *testing.T) {
	mgr := newMgr(t, t.TempDir(), &fakeBuilder{}, stubPublisher(), jobs.Config{MaxConcurrency: 1, BuildTimeout: time.Second})
	_, err := mgr.Enqueue(jobs.Request{Repo: "https://example.com/x.git", Ref: "main"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConcurrentEnqueueIDsUnique(t *testing.T) {
	wsRoot := t.TempDir()
	repo := initFixtureRepo(t)
	mgr := newMgr(t, wsRoot, &fakeBuilder{delay: 10 * time.Millisecond}, stubPublisher(), jobs.Config{
		MaxConcurrency: 4,
		BuildTimeout:   5 * time.Second,
		PushLatest:     true,
	})
	mgr.Start()

	var mu sync.Mutex
	seen := map[string]struct{}{}
	ids := make([]string, 0, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acc, err := mgr.Enqueue(jobs.Request{Repo: repo, Ref: "main"})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			if _, ok := seen[acc.BuildID]; ok {
				t.Errorf("duplicate id %s", acc.BuildID)
			}
			seen[acc.BuildID] = struct{}{}
			ids = append(ids, acc.BuildID)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("unique ids = %d", len(seen))
	}
	waitSucceeded(t, mgr, ids, 10*time.Second)
	mgr.Stop()
}

func waitWorkspaceGone(t *testing.T, dir string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("workspace not cleaned: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitSucceeded(t *testing.T, mgr *jobs.Manager, ids []string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for _, id := range ids {
		for {
			rec, ok := mgr.Get(id)
			if !ok {
				t.Fatalf("missing %s", id)
			}
			if rec.Status == jobs.StatusSucceeded {
				break
			}
			if rec.Status == jobs.StatusFailed || rec.Status == jobs.StatusCanceled {
				msg := ""
				if rec.Error != nil {
					msg = rec.Error.Message
				}
				t.Fatalf("build %s ended %s: %s", id, rec.Status, msg)
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s (status=%s)", id, rec.Status)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func waitTerminal(t *testing.T, mgr *jobs.Manager, id string, timeout time.Duration) *jobs.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rec, ok := mgr.Get(id)
		if !ok {
			t.Fatal("missing record")
		}
		if jobs.IsTerminal(rec.Status) {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout status=%s", rec.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func initFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"forge.yaml": "service:\n  name: api\n  port: 8080\nbuild:\n  dockerfile: Dockerfile\n  context: .\n",
		"Dockerfile": "FROM alpine:3.20\nCMD [\"echo\",\"ok\"]\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=forge", "GIT_AUTHOR_EMAIL=forge@local",
			"GIT_COMMITTER_NAME=forge", "GIT_COMMITTER_EMAIL=forge@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

var _ builder.ImageBuilder = (*fakeBuilder)(nil)
