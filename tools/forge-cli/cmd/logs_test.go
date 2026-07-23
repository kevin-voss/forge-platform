package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/errmap"
)

func TestBuildLogFiltersMatchesObserveQueryParams(t *testing.T) {
	state := &State{Project: "prj_1"}
	f, err := buildLogFilters(state, "dpl_1", "svc_a", "req_9", "trace_abc", "1h", "", "error", 50)
	if err != nil {
		t.Fatal(err)
	}
	q := f.QueryValues()
	want := map[string]string{
		"project":     "prj_1",
		"deployment":  "dpl_1",
		"service":     "svc_a",
		"request_id":  "req_9",
		"trace_id":    "trace_abc",
		"since":       "1h",
		"q":           "error",
		"limit":       "50",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Fatalf("query[%s]=%q want %q (full=%v)", k, q.Get(k), v, q)
		}
	}
}

func TestBuildLogFiltersRequiresScope(t *testing.T) {
	_, err := buildLogFilters(&State{}, "", "", "", "", "", "", "", 10)
	var usage *config.UsageError
	if err == nil || !asUsage(err, &usage) {
		t.Fatalf("err = %v", err)
	}
}

func asUsage(err error, target **config.UsageError) bool {
	u, ok := err.(*config.UsageError)
	if !ok {
		return false
	}
	*target = u
	return true
}

func TestSelectLogSourceFallbackOnlySingleService(t *testing.T) {
	src, err := sharedclient.SelectLogSource("auto", "", true)
	if err == nil {
		t.Fatalf("expected error, got %s", src)
	}
	src, err = sharedclient.SelectLogSource("auto", "svc_1", true)
	if err != nil || src != "runtime" {
		t.Fatalf("src=%s err=%v", src, err)
	}
	src, err = sharedclient.SelectLogSource("auto", "svc_1", false)
	if err != nil || src != "observe" {
		t.Fatalf("src=%s err=%v", src, err)
	}
	_, err = sharedclient.SelectLogSource("runtime", "", false)
	if err == nil {
		t.Fatal("runtime without service should fail")
	}
}

func TestLogsQueryJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("deployment") != "dpl_1" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(sharedclient.LogQueryResult{
			Entries: []sharedclient.LogEntry{{
				Time: "2026-07-23T10:00:00Z", Service: "demo", Level: "info", Message: "hello", Deployment: "dpl_1",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FORGE_OBSERVE_URL", srv.URL)
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CI", "1")

	root := NewRootCommand("test")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"logs", "--deployment", "dpl_1", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("logs: %v", err)
	}
	var result sharedclient.LogQueryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if len(result.Entries) != 1 || result.Entries[0].Message != "hello" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSSEClientResumesFromLastTimestamp(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		since := r.URL.Query().Get("since")
		switch n {
		case 1:
			fmt.Fprintf(w, "event: log\ndata: {\"time\":\"2026-07-23T10:00:01Z\",\"service\":\"s\",\"message\":\"one\",\"level\":\"info\"}\n\n")
			flusher.Flush()
			// Drop the connection to force reconnect.
		default:
			if since != "2026-07-23T10:00:01Z" {
				t.Errorf("resume since = %q", since)
			}
			fmt.Fprintf(w, "event: log\ndata: {\"time\":\"2026-07-23T10:00:02Z\",\"service\":\"s\",\"message\":\"two\",\"level\":\"info\"}\n\n")
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	client, err := sharedclient.NewObserveClient(srv.URL, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	var reconnects atomic.Int64
	client.OnReconnect = func() { reconnects.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var messages []string
	err = client.StreamLogsFollow(ctx, sharedclient.LogFilters{Service: "s"}, 10*time.Millisecond, func(e sharedclient.LogEntry) error {
		messages = append(messages, e.Message)
		if len(messages) >= 2 {
			cancel()
		}
		return nil
	}, nil)
	if err != nil && !errorsIsCancel(err) {
		t.Fatalf("StreamLogsFollow: %v", err)
	}
	if len(messages) < 2 || messages[0] != "one" || messages[1] != "two" {
		t.Fatalf("messages = %#v", messages)
	}
	if reconnects.Load() < 1 {
		t.Fatalf("expected reconnect, got %d", reconnects.Load())
	}
}

func errorsIsCancel(err error) bool {
	return err == context.Canceled || (err != nil && strings.Contains(err.Error(), "context canceled"))
}

func TestLogsFollowFallsBackToRuntime(t *testing.T) {
	observe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"loki_unavailable","message":"down"}}`))
	}))
	t.Cleanup(observe.Close)

	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/workloads/svc_demo/logs") {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("follow") != "true" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: runtime-line-1\n\n")
		flusher.Flush()
	}))
	t.Cleanup(runtime.Close)

	t.Setenv("FORGE_OBSERVE_URL", observe.URL)
	t.Setenv("FORGE_RUNTIME_URL", runtime.URL)
	t.Setenv("FORGE_LOGS_FALLBACK", "auto")
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CI", "1")

	root := NewRootCommand("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"logs", "--service", "svc_demo", "--follow"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- root.ExecuteContext(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stdout.String(), "runtime-line-1") {
			cancel()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	<-errCh
	if !strings.Contains(stdout.String(), "runtime-line-1") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "falling back to runtime") {
		t.Fatalf("stderr missing fallback notice: %q", stderr.String())
	}
}

func TestLogsAuthErrorExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"denied"}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FORGE_OBSERVE_URL", srv.URL)
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := NewRootCommand("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"logs", "--trace-id", "T"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if code := errmap.ExitCode(err); code != errmap.Auth {
		t.Fatalf("exit = %d want %d (%v)", code, errmap.Auth, err)
	}
}
