package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"forge.local/services/forge-build/internal/api"
	"forge.local/services/forge-build/internal/manifest"
	"gopkg.in/yaml.v3"
)

func TestOpenAPIDeclaresBuildPaths(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "openapi", "forge-build.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("paths missing or wrong type: %T", doc["paths"])
	}
	for _, p := range []string{"/v1/builds", "/v1/builds/{buildId}", "/v1/builds/{buildId}/logs", "/v1/builds/{buildId}/cancel"} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("openapi missing path %s", p)
		}
	}
	buildsPath := digMap(t, paths, "/v1/builds")
	if _, ok := buildsPath["get"]; !ok {
		t.Fatal("openapi /v1/builds missing get")
	}
	schemas := digMap(t, doc, "components", "schemas")
	for _, name := range []string{"BuildRequest", "BuildAccepted", "BuildRecord", "ErrorEnvelope", "BuildStatus", "BuildPhase", "BuildError", "CancelAccepted"} {
		if _, ok := schemas[name]; !ok {
			t.Fatalf("openapi missing schema %s", name)
		}
	}
	buildRecord := digMap(t, schemas, "BuildRecord")
	props := digMap(t, buildRecord, "properties")
	for _, field := range []string{"image", "digest", "commit", "error", "phase"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("BuildRecord missing property %s", field)
		}
	}
	statusEnum := digMap(t, schemas, "BuildStatus")
	enums, ok := statusEnum["enum"].([]any)
	if !ok {
		t.Fatalf("BuildStatus enum type %T", statusEnum["enum"])
	}
	hasCanceled := false
	for _, v := range enums {
		if v == "canceled" {
			hasCanceled = true
		}
	}
	if !hasCanceled {
		t.Fatal("BuildStatus missing canceled")
	}
	buildReq := digMap(t, schemas, "BuildRequest")
	reqProps := digMap(t, buildReq, "properties")
	for _, field := range []string{"project", "serviceId", "autoDeploy", "environmentId"} {
		if _, ok := reqProps[field]; !ok {
			t.Fatalf("BuildRequest missing property %s", field)
		}
	}
	for _, field := range []string{"serviceId", "recordedImage", "imageRecorded", "linkedDeploymentId"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("BuildRecord missing property %s", field)
		}
	}
}

func TestExampleForgeYAMLAgainstSchema(t *testing.T) {
	root := repoRoot(t)
	schemaRaw, err := os.ReadFile(filepath.Join(root, "contracts", "examples", "forge.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		t.Fatalf("schema json: %v", err)
	}

	yamlRaw, err := os.ReadFile(filepath.Join(root, "contracts", "examples", "forge.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := yaml.Unmarshal(yamlRaw, &instance); err != nil {
		t.Fatalf("example yaml: %v", err)
	}
	// Normalize YAML numbers to JSON-compatible types for schema checks.
	instance = normalizeYAML(instance)

	if err := validateJSONSchema(schema, instance); err != nil {
		t.Fatalf("example forge.yaml failed schema: %v", err)
	}

	// Cross-check the Go parser accepts the same example.
	if _, err := manifest.Parse(yamlRaw); err != nil {
		t.Fatalf("manifest.Parse example: %v", err)
	}
}

func TestExampleBuildPayloadsMatchDTOs(t *testing.T) {
	root := repoRoot(t)
	reqRaw, err := os.ReadFile(filepath.Join(root, "contracts", "examples", "forge-build-create-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.DecodeBuildRequest(reqRaw, "forge.yaml"); err != nil {
		t.Fatalf("create request: %v", err)
	}

	var accepted api.BuildAccepted
	accRaw, err := os.ReadFile(filepath.Join(root, "contracts", "examples", "forge-build-create-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(accRaw, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Status != api.BuildStatusQueued {
		t.Fatalf("accepted status = %q", accepted.Status)
	}

	var rec api.BuildRecord
	stRaw, err := os.ReadFile(filepath.Join(root, "contracts", "examples", "forge-build-status-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stRaw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != api.BuildStatusSucceeded || rec.Phase != api.BuildPhaseSucceeded {
		t.Fatalf("status = %q phase = %q", rec.Status, rec.Phase)
	}
	if !api.EnforceImageInvariant(rec) {
		t.Fatalf("image invariant violated: %+v", rec)
	}

	var env api.Envelope
	errRaw, err := os.ReadFile(filepath.Join(root, "contracts", "examples", "forge-build-error-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(errRaw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code == "" || env.Error.RequestID == "" || env.Error.Message == "" {
		t.Fatalf("error envelope incomplete: %+v", env.Error)
	}
}

func digMap(t *testing.T, root map[string]any, keys ...string) map[string]any {
	t.Helper()
	cur := root
	for i, key := range keys {
		next, ok := cur[key]
		if !ok {
			t.Fatalf("missing key %q", key)
		}
		m, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("key %q at %d not object: %T", key, i, next)
		}
		cur = m
	}
	return cur
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "openapi", "forge-build.openapi.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("contracts not available from %s (docker build context)", wd)
		}
		dir = parent
	}
}
