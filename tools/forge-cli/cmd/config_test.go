package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/config"
)

func TestConfigCommandsUseTemporaryHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	run := func(args ...string) string {
		t.Helper()
		root := NewRootCommand("test")
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("forge %s: %v", strings.Join(args, " "), err)
		}
		return output.String()
	}

	run("config", "set", "endpoint", "http://127.0.0.1:4001", "--profile", "local")
	run("config", "set", "endpoint", "https://control.staging.example", "--profile", "staging")
	if output := run("config", "use", "staging"); !strings.Contains(output, `using profile "staging"`) {
		t.Fatalf("use output = %q", output)
	}
	if output := run("config", "get", "endpoint"); output != "https://control.staging.example\n" {
		t.Fatalf("get output = %q", output)
	}
	if output := run("config", "list"); !strings.Contains(output, "* staging\thttps://control.staging.example") {
		t.Fatalf("list output = %q", output)
	}

	path := filepath.Join(os.Getenv("HOME"), ".config", "forge", "config.yaml")
	file, err := config.Load(path)
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	if file.CurrentProfile != "staging" || len(file.Profiles) != 2 {
		t.Fatalf("persisted config = %#v", file)
	}
}

func TestVersionAndGlobalFlags(t *testing.T) {
	t.Setenv("FORGE_ENDPOINT", "https://environment.example")
	root := NewRootCommand("v1.2.3")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"version", "--endpoint", "https://flag.example", "--output", "json", "--timeout", "1s"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version command: %v", err)
	}
	if output.String() != "v1.2.3\n" {
		t.Fatalf("version output = %q", output.String())
	}
}
