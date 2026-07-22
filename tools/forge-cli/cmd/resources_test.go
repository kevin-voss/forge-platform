package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/errmap"
)

func TestResourceCommandsRenderJSONAndTables(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/projects":
			if request.Method == http.MethodPost {
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}`))
				return
			}
			_, _ = writer.Write([]byte(`[{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}]`))
		case "/v1/projects/project-1/applications":
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":"app-1","projectId":"project-1","name":"web","createdAt":"now","updatedAt":"now"}`))
		case "/v1/applications/app-1/services":
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":"service-1","applicationId":"app-1","name":"api","port":8080,"createdAt":"now","updatedAt":"now"}`))
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	run := func(args ...string) (string, error) {
		t.Helper()
		root := NewRootCommand("test")
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(append(args, "--endpoint", server.URL))
		err := root.Execute()
		return output.String(), err
	}

	if output, err := run("project", "create", "--name", "acme", "--output", "json"); err != nil || !strings.Contains(output, `"id":"project-1"`) {
		t.Fatalf("project create: output=%q err=%v", output, err)
	}
	if output, err := run("project", "list"); err != nil || !strings.Contains(output, "ID") || !strings.Contains(output, "acme") {
		t.Fatalf("project list: output=%q err=%v", output, err)
	}
	if output, err := run("app", "create", "--project", "project-1", "--name", "web", "--output", "json"); err != nil || !strings.Contains(output, `"id":"app-1"`) {
		t.Fatalf("app create: output=%q err=%v", output, err)
	}
	if output, err := run("service", "create", "--app", "app-1", "--name", "api", "--port", "8080", "--output", "json"); err != nil || !strings.Contains(output, `"port":8080`) {
		t.Fatalf("service create: output=%q err=%v", output, err)
	}
}

func TestResourceCommandsValidateRequiredFlags(t *testing.T) {
	root := NewRootCommand("test")
	root.SetArgs([]string{"service", "create", "--app", "app-1", "--name", "api"})
	err := root.Execute()
	var usageError *config.UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("error = %v, want UsageError", err)
	}
	if usageError.Message != "--port is required" {
		t.Fatalf("message = %q", usageError.Message)
	}
}

func TestResourceCommandsSurfaceRequestIDAndKeepJSONOnStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-Id", "request-success")
		_, _ = writer.Write([]byte(`[{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}]`))
	}))
	defer server.Close()

	root := NewRootCommand("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"project", "list", "--endpoint", server.URL, "--output", "json", "--verbose"})
	if err := root.Execute(); err != nil {
		t.Fatalf("project list: %v", err)
	}
	if got, want := stdout.String(), "[{\"id\":\"project-1\",\"name\":\"acme\",\"slug\":\"acme\",\"createdAt\":\"now\",\"updatedAt\":\"now\"}]\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, "requestId=request-success") || !strings.Contains(got, "duration=") {
		t.Fatalf("stderr = %q, want request ID and duration", got)
	}
}

func TestResourceCommandTimeoutMapsToExitCodeFive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	root := NewRootCommand("test")
	root.SetArgs([]string{"project", "list", "--endpoint", server.URL, "--timeout", "1ms"})
	err := root.Execute()
	if err == nil {
		t.Fatal("project list error = nil, want timeout")
	}
	if got := errmap.ExitCode(err); got != errmap.Timeout {
		t.Fatalf("timeout exit code = %d, want %d; error = %v", got, errmap.Timeout, err)
	}
}

func TestResourceCommandErrorIncludesRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":{"code":"PROJECT_NOT_FOUND","message":"project not found","requestId":"request-missing"}}`))
	}))
	defer server.Close()

	root := NewRootCommand("test")
	root.SetArgs([]string{"project", "get", "missing", "--endpoint", server.URL})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "requestId: request-missing") {
		t.Fatalf("error = %v, want request ID", err)
	}
	if got := errmap.ExitCode(err); got != errmap.NotFound {
		t.Fatalf("exit code = %d, want %d", got, errmap.NotFound)
	}
}
