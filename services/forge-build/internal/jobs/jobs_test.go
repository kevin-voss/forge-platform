package jobs_test

import (
	"context"
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
	"forge.local/services/forge-build/internal/workspace"
)

type fakeBuilder struct {
	started   atomic.Int32
	maxActive atomic.Int32
	active    atomic.Int32
	delay     time.Duration
	err       error
}

func (f *fakeBuilder) Build(ctx context.Context, opts builder.Options, logs *logbuf.Buffer) error {
	f.started.Add(1)
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

func TestQueueRespectsConcurrencyLimit(t *testing.T) {
	wsRoot := t.TempDir()
	ws, err := workspace.New(wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	repo := initFixtureRepo(t)
	fb := &fakeBuilder{delay: 300 * time.Millisecond}
	mgr := jobs.New(jobs.Config{
		MaxConcurrency:   2,
		BuildTimeout:     10 * time.Second,
		LogBufferLines:   100,
		DefaultForgeYAML: "forge.yaml",
	}, ws, fb, slog.Default())
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
	ws, err := workspace.New(wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	repo := initFixtureRepo(t)
	fb := &fakeBuilder{delay: 5 * time.Second}
	mgr := jobs.New(jobs.Config{
		MaxConcurrency:   1,
		BuildTimeout:     200 * time.Millisecond,
		LogBufferLines:   100,
		DefaultForgeYAML: "forge.yaml",
	}, ws, fb, slog.Default())
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
	if !strings.Contains(rec.Error, "timed out") {
		t.Fatalf("error = %q", rec.Error)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
}

func TestBadRefFailsAndCleansWorkspace(t *testing.T) {
	wsRoot := t.TempDir()
	ws, err := workspace.New(wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	repo := initFixtureRepo(t)
	mgr := jobs.New(jobs.Config{
		MaxConcurrency: 1,
		BuildTimeout:   10 * time.Second,
		LogBufferLines: 100,
	}, ws, &fakeBuilder{}, slog.Default())
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
	if !strings.Contains(rec.Error, "clone/checkout") {
		t.Fatalf("error = %q", rec.Error)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, acc.BuildID), 3*time.Second)
	logs := strings.Join(rec.Logs.Snapshot(), "\n")
	if !strings.Contains(logs, "FAILED") {
		t.Fatalf("logs missing failure: %s", logs)
	}
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
			if rec.Status == jobs.StatusFailed {
				t.Fatalf("build %s failed: %s", id, rec.Error)
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
		if rec.Status == jobs.StatusSucceeded || rec.Status == jobs.StatusFailed {
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

// Ensure fakeBuilder satisfies interface at compile time.
var _ builder.ImageBuilder = (*fakeBuilder)(nil)

func TestRejectRemoteRepoOnEnqueue(t *testing.T) {
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mgr := jobs.New(jobs.Config{MaxConcurrency: 1, BuildTimeout: time.Second}, ws, &fakeBuilder{}, slog.Default())
	_, err = mgr.Enqueue(jobs.Request{Repo: "https://example.com/x.git", Ref: "main"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConcurrentEnqueueIDsUnique(t *testing.T) {
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := initFixtureRepo(t)
	mgr := jobs.New(jobs.Config{
		MaxConcurrency: 4,
		BuildTimeout:   5 * time.Second,
	}, ws, &fakeBuilder{delay: 10 * time.Millisecond}, slog.Default())
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
