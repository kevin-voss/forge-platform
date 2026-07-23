package dashboards_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Metric names from the 12.02 instrumentation checklist / control deployment+node metrics.
var allowedMetricRoots = []string{
	"forge_service_up",
	"forge_http_requests_total",
	"forge_http_request_duration_seconds",
	"forge_replicas_ready",
	"forge_reconcile_plan_actions",
	"forge_rollout_step_total",
	"forge_rollout_result_total",
	"forge_deployment_transitions_total",
	"forge_nodes_total",
	"forge_node_free_slots",
	"forge_node_heartbeat_age_seconds",
	"forge_node_offline_total",
	"forge_placements_pending",
}

// Correlation / resource labels that appear in PromQL selectors and by() clauses.
var allowedLabelNames = map[string]bool{
	"forge_service":    true,
	"forge_deployment": true,
	"forge_node":       true,
	"forge_project":    true,
}

var allowedDatasourceUIDs = map[string]bool{
	"prometheus": true,
	"loki":       true,
	"tempo":      true,
}

var forgeMetricRe = regexp.MustCompile(`\bforge_[a-z0-9_]+`)

func TestDashboardJSONValidAndContractParity(t *testing.T) {
	dir := dashboardsDir(t)
	files := []string{"platform.json", "service.json", "deployment.json", "runtime.json"}
	wantUIDs := map[string]string{
		"platform.json":   "forge-platform",
		"service.json":    "forge-service",
		"deployment.json": "forge-deployment",
		"runtime.json":    "forge-runtime",
	}
	wantTitles := map[string]string{
		"platform.json":   "Forge Platform",
		"service.json":    "Forge Service",
		"deployment.json": "Forge Deployment",
		"runtime.json":    "Forge Runtime",
	}

	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var dash map[string]any
		if err := json.Unmarshal(raw, &dash); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		if got, _ := dash["uid"].(string); got != wantUIDs[name] {
			t.Fatalf("%s: uid=%q want %q", name, got, wantUIDs[name])
		}
		if got, _ := dash["title"].(string); got != wantTitles[name] {
			t.Fatalf("%s: title=%q want %q", name, got, wantTitles[name])
		}

		exprs := collectExprs(dash)
		if len(exprs) == 0 {
			t.Fatalf("%s: no panel queries found", name)
		}
		for _, expr := range exprs {
			for _, m := range forgeMetricRe.FindAllString(expr, -1) {
				if !metricAllowed(m) {
					t.Fatalf("%s: panel query references unknown metric %q in %q", name, m, expr)
				}
			}
			for _, uid := range collectDatasourceUIDs(dash) {
				if !allowedDatasourceUIDs[uid] {
					t.Fatalf("%s: unknown datasource uid %q", name, uid)
				}
			}
		}
	}

	// Template variables for correlation labels.
	service := mustLoad(t, filepath.Join(dir, "service.json"))
	if !hasTemplateVar(service, "service") {
		t.Fatal("service.json missing template variable service")
	}
	deployment := mustLoad(t, filepath.Join(dir, "deployment.json"))
	if !hasTemplateVar(deployment, "forge.deployment") {
		t.Fatal("deployment.json missing template variable forge.deployment")
	}
	runtime := mustLoad(t, filepath.Join(dir, "runtime.json"))
	if !hasTemplateVar(runtime, "forge.node") {
		t.Fatal("runtime.json missing template variable forge.node")
	}
}

func TestProviderConfigPointsAtForgeDashboards(t *testing.T) {
	root := repoRoot(t)
	// Canonical provider lives under deploy/ (step 12.03); Compose also loads the
	// identical copy from infrastructure/grafana/provisioning (RO mount root).
	paths := []string{
		filepath.Join(root, "deploy", "observability", "grafana", "provisioning", "dashboards", "forge.yaml"),
		filepath.Join(root, "infrastructure", "grafana", "provisioning", "dashboards", "forge.yaml"),
	}
	var contents []string
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		contents = append(contents, string(raw))
		var doc struct {
			Providers []struct {
				Name    string `yaml:"name"`
				Options struct {
					Path string `yaml:"path"`
				} `yaml:"options"`
			} `yaml:"providers"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if len(doc.Providers) != 1 {
			t.Fatalf("%s: expected 1 provider, got %d", p, len(doc.Providers))
		}
		if doc.Providers[0].Name != "forge" {
			t.Fatalf("%s: provider name=%q", p, doc.Providers[0].Name)
		}
		if doc.Providers[0].Options.Path != "/etc/grafana/forge-dashboards" {
			t.Fatalf("%s: provider path=%q", p, doc.Providers[0].Options.Path)
		}
	}
	if contents[0] != contents[1] {
		t.Fatal("deploy and infrastructure forge.yaml providers must be identical")
	}
}

func TestProvisionedDatasourceUIDs(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "infrastructure", "grafana", "provisioning", "datasources", "datasources.yml"))
	if err != nil {
		t.Fatalf("read datasources.yml: %v", err)
	}
	var doc struct {
		Datasources []struct {
			Name string `yaml:"name"`
			UID  string `yaml:"uid"`
			Type string `yaml:"type"`
		} `yaml:"datasources"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse datasources.yml: %v", err)
	}
	got := map[string]string{}
	for _, ds := range doc.Datasources {
		got[ds.Type] = ds.UID
	}
	for _, want := range []struct{ typ, uid string }{
		{"prometheus", "prometheus"},
		{"loki", "loki"},
		{"tempo", "tempo"},
	} {
		if got[want.typ] != want.uid {
			t.Fatalf("datasource %s uid=%q want %q", want.typ, got[want.typ], want.uid)
		}
	}
}

func metricAllowed(name string) bool {
	if allowedLabelNames[name] {
		return true
	}
	for _, root := range allowedMetricRoots {
		if name == root {
			return true
		}
		// Prometheus/OTEL suffixes for counters and histograms.
		if strings.HasPrefix(name, root+"_") {
			suffix := strings.TrimPrefix(name, root+"_")
			switch suffix {
			case "total", "bucket", "sum", "count",
				"total_bucket", "total_sum", "total_count":
				return true
			}
		}
		// forge_replicas_ready_total / forge_node_free_slots_total (counter export)
		if name == root+"_total" {
			return true
		}
	}
	return false
}

func collectExprs(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		if expr, ok := x["expr"].(string); ok && expr != "" {
			out = append(out, expr)
		}
		for _, child := range x {
			out = append(out, collectExprs(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, collectExprs(child)...)
		}
	}
	return out
}

func collectDatasourceUIDs(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		if ds, ok := x["datasource"].(map[string]any); ok {
			if uid, ok := ds["uid"].(string); ok && uid != "" {
				out = append(out, uid)
			}
		}
		for _, child := range x {
			out = append(out, collectDatasourceUIDs(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, collectDatasourceUIDs(child)...)
		}
	}
	return out
}

func hasTemplateVar(dash map[string]any, name string) bool {
	templating, _ := dash["templating"].(map[string]any)
	list, _ := templating["list"].([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m["name"] == name {
			return true
		}
	}
	return false
}

func mustLoad(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dash map[string]any
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatal(err)
	}
	return dash
}

func dashboardsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "deploy", "observability", "grafana", "dashboards")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
