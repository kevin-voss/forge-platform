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
	for _, p := range []string{"/health/live", "/health/ready", "/", "/v1/events", "/v1/consume"} {
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

	eventsPath, ok := paths["/v1/events"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/events is not an object")
	}
	postEvents, ok := eventsPath["post"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/events missing post")
	}
	if postEvents["operationId"] != "publishEvent" {
		t.Fatalf("publish operationId = %v", postEvents["operationId"])
	}

	consumePath, ok := paths["/v1/consume"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/consume is not an object")
	}
	postConsume, ok := consumePath["post"].(map[string]any)
	if !ok {
		t.Fatal("openapi /v1/consume missing post")
	}
	if postConsume["operationId"] != "consumeEvents" {
		t.Fatalf("consume operationId = %v", postConsume["operationId"])
	}

	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("missing components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("missing schemas")
	}
	for _, name := range []string{
		"Envelope", "PublishRequest", "PublishResponse",
		"ConsumeRequest", "ConsumeResponse", "DeliveredMessage", "ErrorEnvelope",
	} {
		if schemas[name] == nil {
			t.Fatalf("openapi missing schema %s", name)
		}
	}

	envelope, ok := schemas["Envelope"].(map[string]any)
	if !ok {
		t.Fatal("Envelope schema not an object")
	}
	props, ok := envelope["properties"].(map[string]any)
	if !ok {
		t.Fatal("Envelope missing properties")
	}
	for _, field := range []string{"id", "subject", "time", "source", "data"} {
		if props[field] == nil {
			t.Fatalf("Envelope missing property %s", field)
		}
	}
	required, ok := envelope["required"].([]any)
	if !ok {
		t.Fatal("Envelope missing required")
	}
	for _, field := range []string{"id", "subject", "time", "data"} {
		if !containsAny(required, field) {
			t.Fatalf("Envelope required missing %s", field)
		}
	}

	delivered, ok := schemas["DeliveredMessage"].(map[string]any)
	if !ok {
		t.Fatal("DeliveredMessage not an object")
	}
	dprops, ok := delivered["properties"].(map[string]any)
	if !ok {
		t.Fatal("DeliveredMessage missing properties")
	}
	for _, field := range []string{"event_id", "subject", "time", "data", "ack_token"} {
		if dprops[field] == nil {
			t.Fatalf("DeliveredMessage missing property %s", field)
		}
	}
}

func containsAny(items []any, want string) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
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
