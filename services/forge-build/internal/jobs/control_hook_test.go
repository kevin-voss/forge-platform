package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/control"
	"forge.local/services/forge-build/internal/jobs"
	"forge.local/services/forge-build/internal/store"
	"forge.local/services/forge-build/internal/workspace"
)

type fakeControl struct {
	enabled     bool
	recordCalls atomic.Int32
	deployCalls atomic.Int32
	recordErr   error
	deployErr   error
	failTimes   int
	deployID    string
}

func (f *fakeControl) Enabled() bool { return f.enabled }

func (f *fakeControl) RecordImage(_ context.Context, serviceID string, req control.RecordImageRequest) (control.RecordImageResponse, error) {
	n := int(f.recordCalls.Add(1))
	if f.failTimes > 0 && n <= f.failTimes {
		if f.recordErr != nil {
			return control.RecordImageResponse{}, f.recordErr
		}
		return control.RecordImageResponse{}, &control.HTTPError{StatusCode: 503, Body: "down"}
	}
	if f.recordErr != nil {
		return control.RecordImageResponse{}, f.recordErr
	}
	return control.RecordImageResponse{ID: serviceID, Image: req.Image}, nil
}

func (f *fakeControl) CreateDeployment(_ context.Context, serviceID, _ string, req control.CreateDeploymentRequest) (control.DeploymentResponse, error) {
	f.deployCalls.Add(1)
	if f.deployErr != nil {
		return control.DeploymentResponse{}, f.deployErr
	}
	id := f.deployID
	if id == "" {
		id = "dep-1"
	}
	return control.DeploymentResponse{
		ID:            id,
		ServiceID:     serviceID,
		EnvironmentID: req.EnvironmentID,
		Image:         req.Image,
		Status:        "pending",
	}, nil
}

func newMgrWithControl(t *testing.T, wsRoot string, ctrl jobs.ControlClient, cfg jobs.Config) *jobs.Manager {
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
	if cfg.ControlRetries == 0 {
		cfg.ControlRetries = 3
	}
	if cfg.ControlRetryBackoff == 0 {
		cfg.ControlRetryBackoff = 5 * time.Millisecond
	}
	mgr := jobs.NewWithControl(cfg, ws, &fakeBuilder{}, stubPublisher(), ctrl, st, slog.Default())
	if err := mgr.Recover(); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestControlHookOnlyOnSucceeded(t *testing.T) {
	repo := initFixtureRepo(t)

	ctrl := &fakeControl{enabled: true}
	failMgr := newMgrWithControl(t, t.TempDir(), ctrl, jobs.Config{})
	// Replace builder with failing one by constructing a dedicated manager.
	ws, _ := workspace.New(t.TempDir())
	st, _ := store.New(t.TempDir())
	failMgr = jobs.NewWithControl(jobs.Config{
		MaxConcurrency:      1,
		BuildTimeout:        5 * time.Second,
		LogBufferLines:      100,
		DefaultForgeYAML:    "forge.yaml",
		ControlRetries:      2,
		ControlRetryBackoff: 5 * time.Millisecond,
	}, ws, &fakeBuilder{err: errors.New("boom")}, stubPublisher(), ctrl, st, slog.Default())
	_ = failMgr.Recover()
	failMgr.Start()
	defer failMgr.Stop()

	acc, err := failMgr.Enqueue(jobs.Request{
		Repo:      repo,
		Ref:       "main",
		ServiceID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTerminal(t, failMgr, acc.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusFailed {
		t.Fatalf("status=%s", rec.Status)
	}
	if ctrl.recordCalls.Load() != 0 {
		t.Fatalf("record calls on failed build = %d", ctrl.recordCalls.Load())
	}

	okCtrl := &fakeControl{enabled: true}
	mgr := newMgrWithControl(t, t.TempDir(), okCtrl, jobs.Config{})
	mgr.Start()
	defer mgr.Stop()

	acc2, err := mgr.Enqueue(jobs.Request{
		Repo:      repo,
		Ref:       "main",
		ServiceID: "11111111-1111-4111-8111-111111111111",
		Project:   "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec2 := waitControlSettled(t, mgr, acc2.BuildID, 5*time.Second, false)
	if rec2.Status != jobs.StatusSucceeded {
		t.Fatalf("status=%s err=%v", rec2.Status, rec2.Error)
	}
	if !rec2.ImageRecorded || rec2.RecordedImage == "" {
		t.Fatalf("expected recorded image, got %+v", rec2)
	}
	if okCtrl.recordCalls.Load() != 1 {
		t.Fatalf("recordCalls=%d", okCtrl.recordCalls.Load())
	}
	if rec2.Image == "" {
		t.Fatal("pushed image must remain on record")
	}
}

func TestControlHookRetryThenSucceed(t *testing.T) {
	ctrl := &fakeControl{enabled: true, failTimes: 2}
	mgr := newMgrWithControl(t, t.TempDir(), ctrl, jobs.Config{
		ControlRetries:      4,
		ControlRetryBackoff: 5 * time.Millisecond,
	})
	mgr.Start()
	defer mgr.Stop()

	repo := initFixtureRepo(t)
	acc, err := mgr.Enqueue(jobs.Request{
		Repo:      repo,
		Ref:       "main",
		ServiceID: "22222222-2222-4222-8222-222222222222",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitControlSettled(t, mgr, acc.BuildID, 5*time.Second, false)
	if rec.Status != jobs.StatusSucceeded {
		t.Fatalf("status=%s", rec.Status)
	}
	if !rec.ImageRecorded {
		t.Fatal("expected imageRecorded after retries")
	}
	if ctrl.recordCalls.Load() < 3 {
		t.Fatalf("recordCalls=%d want >=3", ctrl.recordCalls.Load())
	}
	if rec.Image == "" {
		t.Fatal("image must not be lost")
	}
}

func TestControlHookDownLeavesImage(t *testing.T) {
	ctrl := &fakeControl{
		enabled:   true,
		recordErr: &control.HTTPError{StatusCode: 503, Body: "unavailable"},
	}
	mgr := newMgrWithControl(t, t.TempDir(), ctrl, jobs.Config{
		ControlRetries:      2,
		ControlRetryBackoff: 5 * time.Millisecond,
	})
	mgr.Start()
	defer mgr.Stop()

	repo := initFixtureRepo(t)
	acc, err := mgr.Enqueue(jobs.Request{
		Repo:      repo,
		Ref:       "main",
		ServiceID: "33333333-3333-4333-8333-333333333333",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitControlSettled(t, mgr, acc.BuildID, 5*time.Second, false)
	if rec.Status != jobs.StatusSucceeded {
		t.Fatalf("status=%s", rec.Status)
	}
	if rec.ImageRecorded {
		t.Fatal("expected imageRecorded=false")
	}
	if rec.Image == "" {
		t.Fatal("image must remain")
	}
	if rec.ControlError == "" {
		t.Fatal("expected controlError")
	}
}

func TestControlAutoDeploy(t *testing.T) {
	ctrl := &fakeControl{enabled: true, deployID: "dep-xyz"}
	mgr := newMgrWithControl(t, t.TempDir(), ctrl, jobs.Config{})
	mgr.Start()
	defer mgr.Stop()

	repo := initFixtureRepo(t)
	acc, err := mgr.Enqueue(jobs.Request{
		Repo:          repo,
		Ref:           "main",
		ServiceID:     "44444444-4444-4444-8444-444444444444",
		AutoDeploy:    true,
		EnvironmentID: "55555555-5555-4555-8555-555555555555",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Wait past image recording until deploy link or control error — ImageRecorded
	// alone races under CI load when CreateDeployment is still in flight.
	rec := waitControlSettled(t, mgr, acc.BuildID, 5*time.Second, true)
	if !rec.ImageRecorded || rec.LinkedDeploymentID != "dep-xyz" {
		t.Fatalf("rec=%+v", rec)
	}
	if ctrl.deployCalls.Load() != 1 {
		t.Fatalf("deployCalls=%d", ctrl.deployCalls.Load())
	}
}

// waitControlSettled waits until the build is terminal and Control linking has
// either succeeded (imageRecorded) or failed (controlError set). When
// requireDeploy is true, also wait for LinkedDeploymentID (or a controlError
// after recording), so AutoDeploy tests do not return mid-flight.
func waitControlSettled(t *testing.T, mgr *jobs.Manager, id string, timeout time.Duration, requireDeploy bool) *jobs.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rec, ok := mgr.Get(id)
		if !ok {
			t.Fatal("missing record")
		}
		if jobs.IsTerminal(rec.Status) {
			if rec.ServiceID == "" || rec.Status != jobs.StatusSucceeded {
				return rec
			}
			if rec.ControlError != "" {
				return rec
			}
			if rec.ImageRecorded {
				if !requireDeploy || rec.LinkedDeploymentID != "" {
					return rec
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout status=%s recorded=%v deploy=%q err=%q",
				rec.Status, rec.ImageRecorded, rec.LinkedDeploymentID, rec.ControlError)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestControlClientIdempotencyViaHTTP(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Idempotency-Key") != "build-bid-1" {
			t.Errorf("key=%q", r.Header.Get("Idempotency-Key"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"sid","image":"localhost:5000/x:1"}`))
	}))
	defer srv.Close()

	c := control.New(srv.URL, srv.Client())
	for i := 0; i < 2; i++ {
		if _, err := c.RecordImage(context.Background(), "sid", control.RecordImageRequest{
			Image: "localhost:5000/x:1", BuildID: "bid-1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}
