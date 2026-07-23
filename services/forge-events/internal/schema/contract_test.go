package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge.local/services/forge-events/internal/schema"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestSchemaFilesAreValidJSONSchema(t *testing.T) {
	dir := platformSchemaDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	metaURL := "https://json-schema.org/draft/2020-12/schema"
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".schema.json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		compiler := jsonschema.NewCompiler()
		compiler.Draft = jsonschema.Draft2020
		url := "file://" + filepath.ToSlash(path)
		if err := compiler.AddResource(url, strings.NewReader(string(raw))); err != nil {
			t.Fatalf("%s add: %v", e.Name(), err)
		}
		if _, err := compiler.Compile(url); err != nil {
			t.Fatalf("%s compile: %v", e.Name(), err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s json: %v", e.Name(), err)
		}
		if doc["$schema"] != metaURL && !strings.Contains(fmtString(doc["$schema"]), "2020-12") {
			t.Fatalf("%s $schema = %v, want draft 2020-12", e.Name(), doc["$schema"])
		}
	}
}

func TestExampleEventsValidateAgainstSchemas(t *testing.T) {
	reg := loadPlatformSchemas(t, schema.ModeStrict)
	examplesDir := filepath.Join(filepath.Dir(platformSchemaDir(t)), "examples", "events")
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("examples dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		subject := strings.TrimSuffix(e.Name(), ".json")
		raw, err := os.ReadFile(filepath.Join(examplesDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := reg.Validate(subject, json.RawMessage(raw), 0); err != nil {
			t.Fatalf("example %s invalid: %v", e.Name(), err)
		}
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}
