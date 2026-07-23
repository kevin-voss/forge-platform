package httpapi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIDeclaresHealthEndpoints(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// services/forge-discovery/internal/httpapi → repo root
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	yamlPath := filepath.Join(root, "contracts/openapi/forge-discovery.openapi.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		// Docker build context is services/forge-discovery only; skip there.
		t.Skipf("openapi not in build context: %v", err)
	}
	text := string(raw)
	for _, needle := range []string{
		"/health/live:",
		"/health/ready:",
		"status: ok",
		"status: not_ready",
		"getHealthLive",
		"getHealthReady",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("openapi missing %q", needle)
		}
	}
}
