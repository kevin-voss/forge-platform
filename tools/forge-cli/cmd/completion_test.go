package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/config"
)

func TestCompletionEmitsScriptsForSupportedShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			root := NewRootCommand("test")
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetArgs([]string{"completion", shell})
			if err := root.Execute(); err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if output.Len() == 0 {
				t.Fatal("completion script is empty")
			}
		})
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	root := NewRootCommand("test")
	root.SetArgs([]string{"completion", "powershell"})
	err := root.Execute()

	var usageError *config.UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("error = %v, want UsageError", err)
	}
	if !strings.Contains(usageError.Message, "bash, zsh, fish") {
		t.Fatalf("message = %q, want supported shells", usageError.Message)
	}
}

func TestProfileCompletionReadsConfiguredProfiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := config.Path()
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if err := config.Save(path, config.File{Profiles: map[string]config.Profile{
		"staging": {Endpoint: "https://staging.example"},
		"local":   {Endpoint: "http://127.0.0.1:4001"},
	}}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, _ := completeProfiles(nil, nil, "")
	if want := []string{"local", "staging"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("profiles = %v, want %v", got, want)
	}
}

func TestOutputCompletionSuggestsSupportedFormats(t *testing.T) {
	root := NewRootCommand("test")
	complete, found := root.GetFlagCompletionFunc("output")
	if !found {
		t.Fatal("output completion is not registered")
	}

	got, _ := complete(root, nil, "")
	if want := []string{"table", "json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("output completions = %v, want %v", got, want)
	}
}

func TestNoInputMissingRequiredFlagReturnsUsageError(t *testing.T) {
	root := NewRootCommand("test")
	root.SetArgs([]string{"--no-input", "project", "create"})
	err := root.Execute()

	var usageError *config.UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("error = %v, want UsageError", err)
	}
	if usageError.Message != "--name is required" {
		t.Fatalf("message = %q", usageError.Message)
	}
}

func TestProfileCompletionReturnsNoValuesForMissingConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "missing"))
	got, _ := completeProfiles(nil, nil, "")
	if len(got) != 0 {
		t.Fatalf("profiles = %v, want none", got)
	}
}

func TestCompletionDoesNotWriteToStderr(t *testing.T) {
	root := NewRootCommand("test")
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs([]string{"completion", "bash"})
	if err := root.Execute(); err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestNoInputEnvironmentMissingRequiredFlagReturnsUsageError(t *testing.T) {
	t.Setenv("FORGE_NO_INPUT", "1")
	root := NewRootCommand("test")
	root.SetArgs([]string{"project", "create"})
	err := root.Execute()

	var usageError *config.UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("error = %v, want UsageError", err)
	}
}

func TestNoInputDoesNotCreateConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := NewRootCommand("test")
	root.SetArgs([]string{"--no-input", "project", "create"})
	_ = root.Execute()
	path, err := config.Path()
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", err)
	}
}
