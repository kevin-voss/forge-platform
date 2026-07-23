package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/errmap"
)

func TestAgentListRunStatusApproveDeny(t *testing.T) {
	var mu sync.Mutex
	runs := map[string]map[string]any{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "req-agent-1")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agents": []map[string]any{
					{
						"name":        "deployment-investigator",
						"model":       "local-general",
						"tools":       []string{"deployment.read", "runtime.restart"},
						"permissions": []string{"deployment:read", "runtime:restart"},
						"limits":      map[string]any{"max_steps": 10, "timeout_seconds": 120},
					},
					{
						"name":        "log-summarizer",
						"model":       "local-general",
						"tools":       []string{"logs.search"},
						"permissions": []string{"logs:read"},
						"limits":      map[string]any{"max_steps": 8, "timeout_seconds": 90},
					},
				},
			})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/agents/") && strings.HasSuffix(r.URL.Path, "/runs"):
			if r.Header.Get("X-Forge-Project") == "" {
				http.Error(w, `{"error":"project required","code":"project_required"}`, http.StatusBadRequest)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Input   string         `json:"input"`
				Context map[string]any `json:"context"`
			}
			_ = json.Unmarshal(body, &req)
			name := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
			name = strings.TrimSuffix(name, "/runs")
			runID := "run-1"
			mu.Lock()
			if name == "deployment-investigator" && req.Context["tool"] == "runtime.restart" {
				runs[runID] = map[string]any{
					"run_id":     runID,
					"agent":      name,
					"project_id": r.Header.Get("X-Forge-Project"),
					"status":     "awaiting_approval",
					"pending_approval": map[string]any{
						"id":     "appr-1",
						"run_id": runID,
						"tool":   "runtime.restart",
						"args":   map[string]any{"deployment_id": "dep-1"},
						"status": "pending",
					},
				}
			} else {
				runs[runID] = map[string]any{
					"run_id":     runID,
					"agent":      name,
					"project_id": r.Header.Get("X-Forge-Project"),
					"status":     "succeeded",
					"result":     req.Input,
					"steps":      []any{},
				}
			}
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": runID, "status": "running"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/runs/"):
			runID := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
			mu.Lock()
			run, ok := runs[runID]
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "run not found", "code": "run_not_found"})
				return
			}
			_ = json.NewEncoder(w).Encode(run)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deny"):
			mu.Lock()
			if run, ok := runs["run-1"]; ok {
				run["status"] = "succeeded"
				delete(run, "pending_approval")
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "denied"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "approved"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FORGE_AGENTS_URL", server.URL)

	root := NewRootCommand("test")
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"agent", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("agent list: %v", err)
	}
	if !strings.Contains(out.String(), "deployment-investigator") || !strings.Contains(out.String(), "log-summarizer") {
		t.Fatalf("list output = %q", out.String())
	}

	out.Reset()
	root = NewRootCommand("test")
	root.SetOut(&out)
	root.SetArgs([]string{
		"agent", "run", "log-summarizer",
		"--project", "proj-a",
		"--input", "errors x3",
		"--dry-run",
		"--poll-interval", "10ms",
		"--json",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("agent run log-summarizer: %v", err)
	}
	if !strings.Contains(out.String(), `"status":"succeeded"`) {
		t.Fatalf("run output = %q", out.String())
	}

	out.Reset()
	root = NewRootCommand("test")
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"agent", "run", "deployment-investigator",
		"--project", "proj-a",
		"--input", "restart",
		"--deployment", "dep-1",
		"--tool", "runtime.restart",
		"--dry-run",
		"--poll-interval", "10ms",
	})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected awaiting_approval error")
	}
	if !IsAwaitingApproval(err) {
		t.Fatalf("error = %v, want AwaitingApprovalError", err)
	}
	if code := errmap.ExitCode(err); code != errmap.AwaitingApprovalExit {
		t.Fatalf("awaiting exit = %d want %d", code, errmap.AwaitingApprovalExit)
	}
	if !strings.Contains(out.String(), "approval_id=appr-1") {
		t.Fatalf("awaiting output = %q", out.String())
	}

	out.Reset()
	root = NewRootCommand("test")
	root.SetOut(&out)
	root.SetArgs([]string{"agent", "deny", "appr-1", "--project", "proj-a", "--reason", "manual"})
	if err := root.Execute(); err != nil {
		t.Fatalf("agent deny: %v", err)
	}
	if !strings.Contains(out.String(), "status=denied") {
		t.Fatalf("deny output = %q", out.String())
	}

	out.Reset()
	root = NewRootCommand("test")
	root.SetOut(&out)
	root.SetArgs([]string{"agent", "status", "run-1", "--project", "proj-a"})
	if err := root.Execute(); err != nil {
		t.Fatalf("agent status: %v", err)
	}
	if !strings.Contains(out.String(), "status=succeeded") {
		t.Fatalf("status output = %q", out.String())
	}
}

func TestAgentRunRequiresProject(t *testing.T) {
	t.Setenv("FORGE_AGENTS_URL", "http://127.0.0.1:9")
	t.Setenv("FORGE_PROJECT", "")
	root := NewRootCommand("test")
	root.SetArgs([]string{"agent", "run", "log-summarizer", "--input", "x", "--wait=false"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("error = %v, want --project required", err)
	}
	if code := errmap.ExitCode(err); code != errmap.Usage {
		t.Fatalf("exit = %d want %d", code, errmap.Usage)
	}
}

func TestAgentUnknownMapsToNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "agent not found", "code": "agent_not_found"})
	}))
	t.Cleanup(server.Close)
	t.Setenv("FORGE_AGENTS_URL", server.URL)

	root := NewRootCommand("test")
	root.SetArgs([]string{"agent", "run", "nope", "--project", "proj-a", "--wait=false"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if code := errmap.ExitCode(err); code != errmap.NotFound {
		t.Fatalf("exit = %d want %d (%v)", code, errmap.NotFound, err)
	}
}

func TestExitForRunStatusCodes(t *testing.T) {
	if err := exitForRunStatus(&sharedclient.RunDetail{ID: "r1", Status: "succeeded"}); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	err := exitForRunStatus(&sharedclient.RunDetail{
		ID:     "r2",
		Status: "awaiting_approval",
		PendingApproval: &sharedclient.PendingApproval{
			ID:   "a1",
			Tool: "runtime.restart",
		},
	})
	if !IsAwaitingApproval(err) {
		t.Fatalf("awaiting: %v", err)
	}
	if code := errmap.ExitCode(err); code != errmap.Generic {
		t.Fatalf("awaiting exit = %d", code)
	}
	err = exitForRunStatus(&sharedclient.RunDetail{ID: "r3", Status: "failed", Error: "timeout"})
	var failed *RunFailedError
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("failed err = %v", err)
	}
	_ = failed
}
