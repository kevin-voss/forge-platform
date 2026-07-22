package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/api"
	"forge.local/services/forge-build/internal/builder"
	"forge.local/services/forge-build/internal/docker"
	"forge.local/services/forge-build/internal/jobs"
	"forge.local/services/forge-build/internal/logbuf"
	"forge.local/services/forge-build/internal/workspace"
)

type instantBuilder struct{}

func (instantBuilder) Build(_ context.Context, opts builder.Options, logs *logbuf.Buffer) error {
	logs.Append("stub-build " + opts.Tag)
	return nil
}

func TestCreateBuildReturns202Contract(t *testing.T) {
	wsRoot := t.TempDir()
	mgr, cleanup := newTestManager(t, wsRoot, instantBuilder{}, 5*time.Second)
	defer cleanup()

	mux := http.NewServeMux()
	api.NewBuildHandler(mgr, "forge.yaml").Register(mux)

	repo := initAPIFixtureRepo(t)
	body := `{"repo":"` + repo + `","ref":"main","forgeYamlPath":"forge.yaml"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/builds", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type=%q", ct)
	}
	if rr.Header().Get("X-Request-Id") == "" {
		t.Fatal("missing X-Request-Id")
	}
	var accepted api.BuildAccepted
	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.BuildID == "" || accepted.Status != api.BuildStatusQueued {
		t.Fatalf("accepted=%+v", accepted)
	}

	logsReq := httptest.NewRequest(http.MethodGet, "/v1/builds/"+accepted.BuildID+"/logs", nil)
	logsRR := httptest.NewRecorder()
	mux.ServeHTTP(logsRR, logsReq)
	if logsRR.Code != http.StatusOK {
		t.Fatalf("logs status=%d", logsRR.Code)
	}
	if ct := logsRR.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("logs content-type=%q", ct)
	}

	// Drain the stub build so Stop does not cancel in-flight work.
	_ = waitBuildViaManager(t, mgr, accepted.BuildID, 5*time.Second)
}

func TestIntegrationDockerBuildAndLogs(t *testing.T) {
	engine := requireDocker(t)
	defer engine.Close()

	wsRoot := t.TempDir()
	mgr, cleanup := newTestManager(t, wsRoot, builder.New(engine), 120*time.Second)
	defer cleanup()

	mux := http.NewServeMux()
	api.NewBuildHandler(mgr, "forge.yaml").Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	repo := initAPIFixtureRepo(t)
	payload, _ := json.Marshal(map[string]string{
		"repo":          "file://" + repo,
		"ref":           "main",
		"forgeYamlPath": "forge.yaml",
	})
	resp, err := http.Post(srv.URL+"/v1/builds", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var accepted api.BuildAccepted
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}

	logsDone := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/builds/"+accepted.BuildID+"/logs?follow=true", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			logsDone <- "err:" + err.Error()
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		logsDone <- string(b)
	}()

	rec := waitBuild(t, srv.URL, accepted.BuildID, 90*time.Second)
	if rec.Status != api.BuildStatusSucceeded {
		t.Fatalf("status=%s error=%s", rec.Status, rec.Error)
	}
	if rec.Image == "" || rec.Commit == "" {
		t.Fatalf("record=%+v", rec)
	}
	exists, err := engine.ImageExists(context.Background(), rec.Image)
	if err != nil || !exists {
		t.Fatalf("image %q exists=%v err=%v", rec.Image, exists, err)
	}
	t.Cleanup(func() { _ = engine.RemoveImage(context.Background(), rec.Image) })

	select {
	case logs := <-logsDone:
		if strings.HasPrefix(logs, "err:") {
			t.Fatal(logs)
		}
		if !strings.Contains(logs, "checked out commit") {
			t.Fatalf("unexpected logs: %s", logs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logs follow did not finish")
	}

	waitWorkspaceGone(t, filepath.Join(wsRoot, accepted.BuildID), 3*time.Second)
}

func TestIntegrationInvalidDockerfileFails(t *testing.T) {
	engine := requireDocker(t)
	defer engine.Close()

	wsRoot := t.TempDir()
	mgr, cleanup := newTestManager(t, wsRoot, builder.New(engine), 60*time.Second)
	defer cleanup()
	mux := http.NewServeMux()
	api.NewBuildHandler(mgr, "forge.yaml").Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	repo := initBrokenDockerfileRepo(t)
	payload, _ := json.Marshal(map[string]string{"repo": repo, "ref": "main"})
	resp, err := http.Post(srv.URL+"/v1/builds", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var accepted api.BuildAccepted
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	rec := waitBuild(t, srv.URL, accepted.BuildID, 60*time.Second)
	if rec.Status != api.BuildStatusFailed {
		t.Fatalf("status=%s error=%s", rec.Status, rec.Error)
	}
	logsResp, err := http.Get(srv.URL + "/v1/builds/" + accepted.BuildID + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer logsResp.Body.Close()
	logs, _ := io.ReadAll(logsResp.Body)
	if !strings.Contains(string(logs), "FAILED") && !strings.Contains(strings.ToLower(string(logs)), "error") {
		t.Fatalf("logs=%s", logs)
	}
	if rec.Image != "" {
		t.Fatalf("unexpected image %q", rec.Image)
	}
	waitWorkspaceGone(t, filepath.Join(wsRoot, accepted.BuildID), 3*time.Second)
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

func newTestManager(t *testing.T, wsRoot string, b builder.ImageBuilder, timeout time.Duration) (*jobs.Manager, func()) {
	t.Helper()
	ws, err := workspace.New(wsRoot)
	if err != nil {
		t.Fatal(err)
	}
	mgr := jobs.New(jobs.Config{
		MaxConcurrency:   2,
		BuildTimeout:     timeout,
		LogBufferLines:   2000,
		DefaultForgeYAML: "forge.yaml",
	}, ws, b, slog.Default())
	mgr.Start()
	return mgr, func() { mgr.Stop() }
}

func requireDocker(t *testing.T) *docker.Client {
	t.Helper()
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c, err := docker.New(host)
	if err != nil {
		t.Skipf("docker client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	return c
}

func waitBuildViaManager(t *testing.T, mgr *jobs.Manager, id string, timeout time.Duration) *jobs.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rec, ok := mgr.Get(id)
		if !ok {
			t.Fatal("missing build")
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

func waitBuild(t *testing.T, base, id string, timeout time.Duration) api.BuildRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(base + "/v1/builds/" + id)
		if err != nil {
			t.Fatal(err)
		}
		var rec api.BuildRecord
		err = json.NewDecoder(resp.Body).Decode(&rec)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status == api.BuildStatusSucceeded || rec.Status == api.BuildStatusFailed {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout status=%s error=%s", rec.Status, rec.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func initAPIFixtureRepo(t *testing.T) string {
	t.Helper()
	return initRepoWithFiles(t, map[string]string{
		"forge.yaml": "service:\n  name: api\n  port: 8080\nbuild:\n  dockerfile: Dockerfile\n  context: .\n",
		"Dockerfile": "FROM alpine:3.20\nLABEL forge.test=1\nCMD [\"echo\",\"ok\"]\n",
	})
}

func initBrokenDockerfileRepo(t *testing.T) string {
	t.Helper()
	return initRepoWithFiles(t, map[string]string{
		"forge.yaml": "service:\n  name: api\n  port: 8080\nbuild:\n  dockerfile: Dockerfile\n  context: .\n",
		"Dockerfile": "FROM alpine:3.20\nRUN /bin/false-does-not-exist\n",
	})
}

func initRepoWithFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
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
