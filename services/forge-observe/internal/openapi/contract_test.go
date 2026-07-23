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
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "openapi", "forge-observe.openapi.yaml"))
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
	for _, p := range []string{"/health/live", "/health/ready", "/", "/v1/health/backends", "/v1/logs", "/v1/logs/stream", "/v1/alerts"} {
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

	backendsPath, ok := paths["/v1/health/backends"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/health/backends is not an object")
	}
	getBackends, ok := backendsPath["get"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/health/backends missing get")
	}
	if getBackends["operationId"] != "getBackendHealth" {
		t.Fatalf("backends operationId = %v", getBackends["operationId"])
	}

	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("missing components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("missing schemas")
	}
	for _, name := range []string{"HealthStatus", "Identity", "BackendHealth", "LogEntry", "LogQueryResult", "AlertStatus", "Error"} {
		if schemas[name] == nil {
			t.Fatalf("missing schema %s", name)
		}
	}

	logsPath, ok := paths["/v1/logs"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/logs is not an object")
	}
	getLogs, ok := logsPath["get"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/logs missing get")
	}
	if getLogs["operationId"] != "queryLogs" {
		t.Fatalf("logs operationId = %v", getLogs["operationId"])
	}

	streamPath, ok := paths["/v1/logs/stream"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/logs/stream is not an object")
	}
	getStream, ok := streamPath["get"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/logs/stream missing get")
	}
	if getStream["operationId"] != "streamLogs" {
		t.Fatalf("stream operationId = %v", getStream["operationId"])
	}

	alertsPath, ok := paths["/v1/alerts"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/alerts is not an object")
	}
	getAlerts, ok := alertsPath["get"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/alerts missing get")
	}
	if getAlerts["operationId"] != "listAlerts" {
		t.Fatalf("alerts operationId = %v", getAlerts["operationId"])
	}

	logEntry, ok := schemas["LogEntry"].(map[string]any)
	if !ok {
		t.Fatal("LogEntry schema missing")
	}
	props, ok := logEntry["properties"].(map[string]any)
	if !ok {
		t.Fatal("LogEntry properties missing")
	}
	for _, field := range []string{"time", "service", "trace_id", "request_id", "message", "deployment"} {
		if props[field] == nil {
			t.Fatalf("LogEntry missing correlation field %s", field)
		}
	}
}

func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "openapi", "forge-observe.openapi.yaml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
