package schema_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"forge.local/services/forge-events/internal/schema"
)

func TestValidApplicationCrashedPasses(t *testing.T) {
	reg := loadPlatformSchemas(t, schema.ModeStrict)
	data := json.RawMessage(`{"service":"demo","reason":"oom","occurred_at":"2026-07-22T14:00:00Z"}`)
	if err := reg.Validate("application.crashed", data, 0); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestMissingRequiredFieldViolations(t *testing.T) {
	reg := loadPlatformSchemas(t, schema.ModeStrict)
	err := reg.Validate("application.crashed", json.RawMessage(`{"reason":"oom"}`), 0)
	if err == nil {
		t.Fatal("expected validation error")
	}
	var ve *schema.Error
	if !errors.As(err, &ve) {
		t.Fatalf("err type = %T, want *schema.Error", err)
	}
	if ve.Reason != "validation_failed" {
		t.Fatalf("reason = %q", ve.Reason)
	}
	if len(ve.Violations) == 0 {
		t.Fatal("expected violations")
	}
	if !errors.Is(err, schema.ErrValidationFailed) {
		t.Fatalf("unwrap = %v", err)
	}
}

func TestUnknownSubjectStrictRejectedWarnAllowed(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "application.crashed.schema.json", "application.crashed", 1,
		`{"type":"object","additionalProperties":true}`)

	strict := schema.NewRegistry(schema.ModeStrict, nil, nil)
	if err := strict.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	err := strict.Validate("application.unknown", json.RawMessage(`{}`), 0)
	if err == nil {
		t.Fatal("strict: expected unknown schema error")
	}
	var ve *schema.Error
	if !errors.As(err, &ve) || ve.Reason != "unknown_schema" {
		t.Fatalf("strict err = %#v", err)
	}

	warn := schema.NewRegistry(schema.ModeWarn, nil, nil)
	if err := warn.Load(dir); err != nil {
		t.Fatalf("Load warn: %v", err)
	}
	if err := warn.Validate("application.unknown", json.RawMessage(`{}`), 0); err != nil {
		t.Fatalf("warn: unexpected err %v", err)
	}
}

func TestSchemaVersioningSelectsV1VsV2(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "demo.event.v1.schema.json", "demo.event", 1, `{
		"type":"object",
		"additionalProperties":false,
		"required":["name"],
		"properties":{"name":{"type":"string"}}
	}`)
	writeSchema(t, dir, "demo.event.v2.schema.json", "demo.event", 2, `{
		"type":"object",
		"additionalProperties":false,
		"required":["name","kind"],
		"properties":{"name":{"type":"string"},"kind":{"type":"string"}}
	}`)

	reg := schema.NewRegistry(schema.ModeStrict, nil, nil)
	if err := reg.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	v1Only := json.RawMessage(`{"name":"x"}`)
	if err := reg.Validate("demo.event", v1Only, 1); err != nil {
		t.Fatalf("v1 validate: %v", err)
	}
	if err := reg.Validate("demo.event", v1Only, 2); err == nil {
		t.Fatal("v2 should reject missing kind")
	}
	both := json.RawMessage(`{"name":"x","kind":"a"}`)
	if err := reg.Validate("demo.event", both, 2); err != nil {
		t.Fatalf("v2 validate: %v", err)
	}
	if err := reg.Validate("demo.event", v1Only, 0); err == nil {
		t.Fatal("latest (v2) should reject missing kind")
	}
}

func TestBrokenSchemaFileFailsReady(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.schema.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := schema.NewRegistry(schema.ModeStrict, nil, nil)
	if err := reg.Load(dir); err == nil {
		t.Fatal("expected load error")
	}
	if err := reg.ReadyError(); err == nil {
		t.Fatal("expected ReadyError after failed load")
	}
}

func TestListAndGetSchemas(t *testing.T) {
	reg := loadPlatformSchemas(t, schema.ModeStrict)
	list := reg.List()
	if _, ok := list["application.crashed"]; !ok {
		t.Fatalf("list missing application.crashed: %#v", list)
	}
	if list["application.crashed"].LatestVersion != 1 {
		t.Fatalf("latest = %d", list["application.crashed"].LatestVersion)
	}
	detail, err := reg.Get("application.crashed")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.Subject != "application.crashed" || len(detail.Versions) != 1 {
		t.Fatalf("detail = %#v", detail)
	}
}

func loadPlatformSchemas(t *testing.T, mode schema.Mode) *schema.Registry {
	t.Helper()
	dir := platformSchemaDir(t)
	reg := schema.NewRegistry(mode, nil, nil)
	if err := reg.Load(dir); err != nil {
		t.Fatalf("Load %s: %v", dir, err)
	}
	if err := reg.ReadyError(); err != nil {
		t.Fatalf("ReadyError: %v", err)
	}
	return reg
}

func platformSchemaDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "contracts", "events")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("contracts/events not available in this build context")
		}
		dir = parent
	}
}

func writeSchema(t *testing.T, dir, name, subject string, version int, body string) {
	t.Helper()
	raw := fmt.Sprintf(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://forge.local/test/%s/v%d",
  "title": %q,
  "x-forge-subject": %q,
  "x-forge-schema-version": %d,
  %s
}`, name, version, subject, subject, version, trimObjectWrapper(body))
	if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func trimObjectWrapper(body string) string {
	body = trimSpace(body)
	if len(body) >= 2 && body[0] == '{' && body[len(body)-1] == '}' {
		return body[1 : len(body)-1]
	}
	return body
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
