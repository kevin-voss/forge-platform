package correlation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge.local/services/forge-observe/internal/correlation"
)

func TestConstantsMatchDocumentedContract(t *testing.T) {
	raw, err := os.ReadFile(contractPath(t))
	if err != nil {
		t.Fatalf("read correlation contract: %v", err)
	}
	doc := string(raw)

	for _, item := range []struct {
		name  string
		value string
	}{
		{"HeaderTraceparent", correlation.HeaderTraceparent},
		{"HeaderRequestID", correlation.HeaderRequestID},
		{"AttrProject", correlation.AttrProject},
		{"AttrDeployment", correlation.AttrDeployment},
		{"AttrService", correlation.AttrService},
		{"AttrNode", correlation.AttrNode},
		{"LogTraceID", correlation.LogTraceID},
		{"LogSpanID", correlation.LogSpanID},
		{"LogRequestID", correlation.LogRequestID},
	} {
		if !strings.Contains(doc, "`"+item.value+"`") && !strings.Contains(doc, item.value) {
			t.Fatalf("contract doc missing %s value %q", item.name, item.value)
		}
	}

	for _, h := range correlation.RequiredHeaders {
		if !strings.Contains(doc, h) {
			t.Fatalf("RequiredHeaders entry %q missing from contract", h)
		}
	}
	for _, a := range correlation.RequiredResourceAttributes {
		if !strings.Contains(doc, a) {
			t.Fatalf("RequiredResourceAttributes entry %q missing from contract", a)
		}
	}
	for _, f := range correlation.RequiredLogFields {
		if !strings.Contains(doc, f) {
			t.Fatalf("RequiredLogFields entry %q missing from contract", f)
		}
	}
}

func contractPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getcwd: %v", err)
	}
	for {
		p := filepath.Join(dir, "docs", "contracts", "observability-correlation.md")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("observability-correlation.md not found walking up from package dir")
		}
		dir = parent
	}
}
