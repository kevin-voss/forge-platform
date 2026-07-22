package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidManifest(t *testing.T) {
	raw := []byte(`
service:
  name: api
  port: 8080
build:
  dockerfile: Dockerfile
  context: .
`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Service.Name != "api" || m.Service.Port != 8080 {
		t.Fatalf("service = %+v", m.Service)
	}
	if m.Build.Dockerfile != "Dockerfile" || m.Build.Context != "." {
		t.Fatalf("build = %+v", m.Build)
	}
	t.Logf("manifest validation ok: service=%s dockerfile=%s", m.Service.Name, m.Build.Dockerfile)
}

func TestParseFileExample(t *testing.T) {
	path := filepath.Join(repoRoot(t), "contracts", "examples", "forge.yaml.example")
	m, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", path, err)
	}
	if m.Service.Name != "api" || m.Service.Port != 8080 {
		t.Fatalf("unexpected example manifest: %+v", m)
	}
}

func TestParseTableDriven(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		field   string
		wantErr bool
	}{
		{
			name: "valid nested context",
			yaml: `
service:
  name: worker
  port: 1
build:
  dockerfile: docker/Dockerfile
  context: ./app
`,
		},
		{
			name: "missing service name",
			yaml: `
service:
  name: ""
  port: 8080
build:
  dockerfile: Dockerfile
  context: .
`,
			field:   "service.name",
			wantErr: true,
		},
		{
			name: "missing dockerfile",
			yaml: `
service:
  name: api
  port: 8080
build:
  dockerfile: ""
  context: .
`,
			field:   "build.dockerfile",
			wantErr: true,
		},
		{
			name: "bad port zero",
			yaml: `
service:
  name: api
  port: 0
build:
  dockerfile: Dockerfile
  context: .
`,
			field:   "service.port",
			wantErr: true,
		},
		{
			name: "bad port high",
			yaml: `
service:
  name: api
  port: 70000
build:
  dockerfile: Dockerfile
  context: .
`,
			field:   "service.port",
			wantErr: true,
		},
		{
			name: "bad service name",
			yaml: `
service:
  name: Api_Service
  port: 8080
build:
  dockerfile: Dockerfile
  context: .
`,
			field:   "service.name",
			wantErr: true,
		},
		{
			name: "dockerfile path traversal",
			yaml: `
service:
  name: api
  port: 8080
build:
  dockerfile: ../Dockerfile
  context: .
`,
			field:   "build.dockerfile",
			wantErr: true,
		},
		{
			name: "context path traversal",
			yaml: `
service:
  name: api
  port: 8080
build:
  dockerfile: Dockerfile
  context: ../../etc
`,
			field:   "build.context",
			wantErr: true,
		},
		{
			name: "absolute dockerfile",
			yaml: `
service:
  name: api
  port: 8080
build:
  dockerfile: /etc/passwd
  context: .
`,
			field:   "build.dockerfile",
			wantErr: true,
		},
		{
			name: "absolute context",
			yaml: `
service:
  name: api
  port: 8080
build:
  dockerfile: Dockerfile
  context: /tmp
`,
			field:   "build.context",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected validation error")
				}
				ve, ok := AsValidationError(err)
				if !ok {
					t.Fatalf("expected ValidationError, got %T: %v", err, err)
				}
				if ve.Field != tc.field {
					t.Fatalf("field = %q, want %q (err=%v)", ve.Field, tc.field, err)
				}
				t.Logf("rejected as expected: %v details=%v", err, ve.Details())
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
		})
	}
}

func TestResolvePathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolvePath(root, "../outside"); err == nil {
		t.Fatal("expected escape rejection")
	}
	abs, err := ResolvePath(root, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolvePath: %v", err)
	}
	want := filepath.Join(root, "Dockerfile")
	if abs != want {
		t.Fatalf("ResolvePath = %q, want %q", abs, want)
	}
}

func TestValidationErrorDetails(t *testing.T) {
	err := &ValidationError{Field: "service.port", Message: "port must be an integer between 1 and 65535"}
	d := err.Details()
	if d["field"] != "service.port" || !strings.Contains(d["reason"], "port") {
		t.Fatalf("details = %#v", d)
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
		if _, err := os.Stat(filepath.Join(dir, "contracts", "examples", "forge.yaml.example")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("contracts not available from %s (docker build context)", wd)
		}
		dir = parent
	}
}
