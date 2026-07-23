package openapi_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPISkeletonPaths(t *testing.T) {
	root, ok := repoRoot()
	if !ok {
		t.Skip("openapi contract file not available in this build context")
	}
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "openapi", "forge-events.openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("openapi yaml parse: %v", err)
	}
	if doc["openapi"] == nil {
		t.Fatal("missing openapi version")
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("missing paths")
	}
	for _, p := range []string{"/health/live", "/health/ready", "/"} {
		if paths[p] == nil {
			t.Fatalf("openapi missing path %s", p)
		}
	}
	identity, ok := paths["/"].(map[string]any)
	if !ok {
		t.Fatal("openapi / is not an object")
	}
	get, ok := identity["get"].(map[string]any)
	if !ok {
		t.Fatal("openapi / missing get")
	}
	if get["operationId"] != "getIdentity" {
		t.Fatalf("operationId = %v, want getIdentity", get["operationId"])
	}
}

func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "openapi", "forge-events.openapi.yaml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
