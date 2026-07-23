package httpapi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIDeclaresHealthLeaseListAndWatch(t *testing.T) {
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
		"registerEndpoint",
		"renewEndpoint",
		"deregisterEndpoint",
		"listServiceEndpoints",
		"watchServiceEndpoints",
		"/renew:",
		"leaseSeconds",
		"expiresAt",
		"RegisterEndpointRequest",
		"RenewEndpointRequest",
		"name: ready",
		"name: revision",
		"name: since",
		"text/event-stream",
		"endpoints/watch:",
		"event: added",
		"event: updated",
		"event: removed",
		"EndpointWatchEventPayload",
		"resourceVersion",
		"GET /v1/watch/endpoints",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("openapi missing %q", needle)
		}
	}
}
