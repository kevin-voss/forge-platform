package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildRequestDecodeAndSerialize(t *testing.T) {
	path := filepath.Join(repoRoot(t), "contracts", "examples", "forge-build-create-request.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	req, err := DecodeBuildRequest(raw, "forge.yaml")
	if err != nil {
		t.Fatalf("DecodeBuildRequest: %v", err)
	}
	if req.Repo != "file:///fixtures/app" || req.Ref != "main" || req.ForgeYamlPath != "forge.yaml" {
		t.Fatalf("unexpected request: %+v", req)
	}

	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var again BuildRequest
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if again != req {
		t.Fatalf("round-trip mismatch: %+v vs %+v", again, req)
	}
}

func TestBuildAcceptedAndRecordShapes(t *testing.T) {
	acceptedRaw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "examples", "forge-build-create-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var accepted BuildAccepted
	if err := json.Unmarshal(acceptedRaw, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.BuildID == "" || accepted.Status != BuildStatusQueued {
		t.Fatalf("accepted = %+v", accepted)
	}

	statusRaw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "examples", "forge-build-status-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec BuildRecord
	if err := json.Unmarshal(statusRaw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != BuildStatusSucceeded || rec.Image == "" || rec.FinishedAt == nil {
		t.Fatalf("record = %+v", rec)
	}
	if rec.StartedAt.IsZero() {
		t.Fatal("startedAt zero")
	}

	// Ensure omitempty matches OpenAPI optional fields when re-marshaled.
	minimal := BuildRecord{
		BuildID:   "22222222-2222-4222-8222-222222222222",
		Status:    BuildStatusQueued,
		StartedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(minimal)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"image", "commit", "finishedAt", "error"} {
		if _, ok := m[key]; ok {
			t.Fatalf("expected %s omitted, got %v", key, m)
		}
	}
}

func TestBuildRequestValidation(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		field string
	}{
		{name: "missing repo", raw: `{"ref":"main"}`, field: "repo"},
		{name: "missing ref", raw: `{"repo":"file:///x"}`, field: "ref"},
		{name: "traversal forgeYamlPath", raw: `{"repo":"file:///x","ref":"main","forgeYamlPath":"../forge.yaml"}`, field: "forgeYamlPath"},
		{name: "absolute forgeYamlPath", raw: `{"repo":"file:///x","ref":"main","forgeYamlPath":"/tmp/forge.yaml"}`, field: "forgeYamlPath"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBuildRequest([]byte(tc.raw), "forge.yaml")
			if err == nil {
				t.Fatal("expected error")
			}
			env := ValidationEnvelope(err, "req_test")
			if env.Error.Code != "validation_error" || env.Error.RequestID != "req_test" {
				t.Fatalf("envelope = %+v", env.Error)
			}
			if env.Error.Details["field"] != tc.field {
				t.Fatalf("details field = %q, want %q (env=%+v)", env.Error.Details["field"], tc.field, env.Error)
			}
		})
	}
}

func TestWriteValidationEnvelope(t *testing.T) {
	_, err := DecodeBuildRequest([]byte(`{"repo":"","ref":"main"}`), "forge.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
	rr := httptest.NewRecorder()
	WriteValidation(rr, err)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "validation_error" || env.Error.RequestID == "" {
		t.Fatalf("envelope = %+v", env)
	}
	if rr.Header().Get("X-Request-Id") == "" {
		t.Fatal("missing X-Request-Id")
	}

	errRaw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "examples", "forge-build-error-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var example Envelope
	if err := json.Unmarshal(errRaw, &example); err != nil {
		t.Fatal(err)
	}
	if example.Error.Code != "validation_error" || example.Error.Details["field"] == "" {
		t.Fatalf("example envelope = %+v", example)
	}
}

func TestEffectiveForgeYAMLPath(t *testing.T) {
	req := BuildRequest{Repo: "file:///x", Ref: "main"}
	if got := req.EffectiveForgeYAMLPath(""); got != "forge.yaml" {
		t.Fatalf("default = %q", got)
	}
	if got := req.EffectiveForgeYAMLPath("custom.yaml"); got != "custom.yaml" {
		t.Fatalf("cfg default = %q", got)
	}
	req.ForgeYamlPath = "svc/forge.yaml"
	if got := req.EffectiveForgeYAMLPath("custom.yaml"); got != "svc/forge.yaml" {
		t.Fatalf("override = %q", got)
	}
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
			t.Fatal("could not find repo root from", wd)
		}
		dir = parent
	}
}
