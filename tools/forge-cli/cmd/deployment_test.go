package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/config"
)

const deploymentJSON = `{"id":"deployment-1","serviceId":"service-1","environmentId":"env-1","image":"registry.example/api:1","desiredReplicas":1,"status":"pending","createdAt":"now","updatedAt":"now"}`

func TestDeploymentCommandsCreateStatusAndList(t *testing.T) {
	createdByKey := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/services/service-1/deployments":
			if request.Method == http.MethodPost {
				createdByKey[request.Header.Get("Idempotency-Key")] = true
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(deploymentJSON))
				return
			}
			_, _ = writer.Write([]byte("[" + deploymentJSON + "]"))
		case "/v1/deployments/deployment-1":
			_, _ = writer.Write([]byte(deploymentJSON))
		case "/v1/services/missing/deployments":
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"error":{"code":"SERVICE_NOT_FOUND","message":"service not found","requestId":"request-404"}}`))
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

	createArgs := []string{
		"deployment", "create",
		"--service", "service-1",
		"--image", "registry.example/api:1",
		"--env", "env-1",
		"--idempotency-key", "retry-key",
		"--output", "json",
	}
	for range 2 {
		if output, err := run(createArgs...); err != nil || !strings.Contains(output, `"status":"pending"`) {
			t.Fatalf("deployment create: output=%q err=%v", output, err)
		}
	}
	if len(createdByKey) != 1 || !createdByKey["retry-key"] {
		t.Fatalf("idempotent creates = %#v", createdByKey)
	}
	if output, err := run("deployment", "status", "deployment-1"); err != nil || !strings.Contains(output, "STATUS") || !strings.Contains(output, "pending") {
		t.Fatalf("deployment status: output=%q err=%v", output, err)
	}
	if output, err := run("deployment", "list", "--service", "service-1", "--output", "json"); err != nil || !strings.Contains(output, `"id":"deployment-1"`) {
		t.Fatalf("deployment list: output=%q err=%v", output, err)
	}
	if _, err := run("deployment", "list", "--service", "missing"); err == nil || !strings.Contains(err.Error(), "service not found") {
		t.Fatalf("unknown service error = %v", err)
	}
}

func TestDeploymentCreateValidatesRequiredFlagsAndGeneratesUUID(t *testing.T) {
	root := NewRootCommand("test")
	root.SetArgs([]string{"deployment", "create", "--service", "service-1", "--image", "image"})
	err := root.Execute()
	var usageError *config.UsageError
	if !errors.As(err, &usageError) || usageError.Message != "--env is required" {
		t.Fatalf("error = %v, want missing --env UsageError", err)
	}

	key, err := newIdempotencyKey()
	if err != nil {
		t.Fatalf("newIdempotencyKey() error = %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(key) {
		t.Fatalf("idempotency key = %q, want UUID v4", key)
	}
}
