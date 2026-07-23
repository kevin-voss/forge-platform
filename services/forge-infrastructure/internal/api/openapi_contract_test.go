package api_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIDeclaresHealthAndOperations(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	yamlPath := filepath.Join(root, "contracts/openapi/forge-infrastructure.openapi.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Skipf("openapi not in build context: %v", err)
	}
	text := string(raw)
	for _, needle := range []string{
		"/health/live:",
		"/health/ready:",
		"getHealthLive",
		"getHealthReady",
		"/v1/operations/{opId}:",
		"getOperation",
		"status: ok",
		"pending",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("openapi missing %q", needle)
		}
	}
}
