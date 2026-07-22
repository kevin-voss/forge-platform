package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/config"
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
