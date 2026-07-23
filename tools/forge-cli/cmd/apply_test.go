package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyCommandDryRunPostsManifest(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/apply" || request.Method != http.MethodPost {
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"operationId":"apl_test",
			"dryRun":true,
			"changedCount":1,
			"results":[{"kind":"Application","name":"invoice-api","action":"create"}]
		}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	manifest := filepath.Join(dir, "forge.yaml")
	content := `
apiVersion: forge.dev/v1
kind: Application
metadata:
  name: invoice-api
  project: invoice-platform
  environment: production
spec:
  image: registry.forge.internal/invoice-api:1.0.0
`
	if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"apply", "-f", manifest, "--dry-run", "--endpoint", server.URL, "--output", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if gotBody["dryRun"] != true {
		t.Fatalf("dryRun = %v", gotBody["dryRun"])
	}
	resources, _ := gotBody["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("resources len = %d", len(resources))
	}
	if !strings.Contains(output.String(), `"operationId":"apl_test"`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestApplyRequiresFilename(t *testing.T) {
	root := NewRootCommand("test")
	root.SetArgs([]string{"apply", "--endpoint", "http://127.0.0.1:9"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "-f") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadManifestResourcesMultiDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.yaml")
	content := `
apiVersion: forge.dev/v1
kind: Application
metadata:
  name: a
  project: p
  environment: e
spec: {}
---
apiVersion: forge.dev/v1
kind: Application
metadata:
  name: b
  project: p
  environment: e
spec: {}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := loadManifestResources(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("len = %d", len(docs))
	}
}
