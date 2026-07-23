package alerts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAlertRulesReferenceChecklistMetrics(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "observability", "prometheus", "rules", "forge-alerts.yml"))
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	var doc struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert string `yaml:"alert"`
				Expr  string `yaml:"expr"`
				For   string `yaml:"for"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse rules yaml: %v", err)
	}

	wantAlerts := map[string]bool{
		"ServiceDown":   false,
		"HighErrorRate": false,
	}
	var exprs []string
	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			if _, ok := wantAlerts[r.Alert]; ok {
				wantAlerts[r.Alert] = true
			}
			if strings.TrimSpace(r.Expr) == "" {
				t.Fatalf("alert %s missing expr", r.Alert)
			}
			if strings.TrimSpace(r.For) == "" {
				t.Fatalf("alert %s missing for", r.Alert)
			}
			exprs = append(exprs, r.Expr)
		}
	}
	for name, found := range wantAlerts {
		if !found {
			t.Fatalf("missing alert rule %s", name)
		}
	}
	joined := strings.Join(exprs, "\n")
	if !strings.Contains(joined, "forge_service_up") {
		t.Fatal("ServiceDown must reference forge_service_up")
	}
	if !strings.Contains(joined, "forge_http_requests_total") {
		t.Fatal("HighErrorRate must reference forge_http_requests_total")
	}
	if !strings.Contains(joined, `http_status_class="5xx"`) && !strings.Contains(joined, `http_status_class='5xx'`) {
		t.Fatal("HighErrorRate must filter http_status_class=5xx")
	}
}

func TestAlertRulesPromtoolCheckAndUnitTests(t *testing.T) {
	if _, err := exec.LookPath("promtool"); err != nil {
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("promtool/docker not available in this environment")
		}
	}

	root := repoRoot(t)
	badDir := t.TempDir()
	bad := filepath.Join(badDir, "bad.yml")
	if err := os.WriteFile(bad, []byte("groups:\n  - name: x\n    rules:\n      - alert: Broken\n        expr: '((('\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPromtool(t, root, badDir, "check", "rules", "/work/bad.yml"); err == nil {
		t.Fatal("expected promtool check rules to fail on syntax error")
	}

	obs := filepath.Join(root, "deploy", "observability", "prometheus")
	if err := runPromtool(t, root, obs, "check", "rules", "/work/rules/forge-alerts.yml"); err != nil {
		t.Fatalf("promtool check rules: %v", err)
	}
	if err := runPromtool(t, root, obs, "test", "rules", "/work/rule-tests/forge-alerts_test.yml"); err != nil {
		t.Fatalf("promtool test rules: %v", err)
	}
}

func runPromtool(t *testing.T, _ string, mountDir string, args ...string) error {
	t.Helper()
	if path, err := exec.LookPath("promtool"); err == nil {
		localArgs := make([]string, len(args))
		copy(localArgs, args)
		for i, a := range localArgs {
			if strings.HasPrefix(a, "/work/") {
				localArgs[i] = filepath.Join(mountDir, strings.TrimPrefix(a, "/work/"))
			}
		}
		cmd := exec.Command(path, localArgs...)
		cmd.Dir = mountDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	}

	dockerArgs := []string{
		"run", "--rm",
		"--entrypoint", "promtool",
		"-v", mountDir + ":/work:ro",
		"-w", "/work",
		"prom/prometheus:v2.55.1",
	}
	dockerArgs = append(dockerArgs, args...)
	cmd := exec.Command("docker", dockerArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "deploy", "observability", "prometheus", "rules", "forge-alerts.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
