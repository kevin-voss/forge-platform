package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"forge.local/services/forge-build/internal/git"
)

func TestValidateRepoLocalOnly(t *testing.T) {
	if _, err := git.ValidateRepo("https://example.com/repo.git"); err == nil {
		t.Fatal("expected remote URL rejection")
	}
	if _, err := git.ValidateRepo("relative/path"); err == nil {
		t.Fatal("expected relative path rejection")
	}
	dir := t.TempDir()
	got, err := git.ValidateRepo(dir)
	if err != nil || got != filepath.Clean(dir) {
		t.Fatalf("abs path: got %q err=%v", got, err)
	}
	got, err = git.ValidateRepo("file://" + dir)
	if err != nil || got != filepath.Clean(dir) {
		t.Fatalf("file URL: got %q err=%v", got, err)
	}
}

func TestCloneCheckoutLocalFixture(t *testing.T) {
	repo := initFixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")
	result, err := git.CloneCheckout(context.Background(), "file://"+repo, "main", dest)
	if err != nil {
		t.Fatalf("CloneCheckout: %v", err)
	}
	if result.Commit == "" {
		t.Fatal("empty commit")
	}
	body, err := os.ReadFile(filepath.Join(dest, "forge.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "name: api") {
		t.Fatalf("forge.yaml content = %q", body)
	}

	// Bad ref fails clearly.
	badDest := filepath.Join(t.TempDir(), "bad")
	if _, err := git.CloneCheckout(context.Background(), repo, "no-such-ref", badDest); err == nil {
		t.Fatal("expected bad ref error")
	}
}

func initFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("forge.yaml", "service:\n  name: api\n  port: 8080\nbuild:\n  dockerfile: Dockerfile\n  context: .\n")
	write("Dockerfile", "FROM alpine:3.20\nCMD [\"echo\",\"ok\"]\n")
	write("README.md", "fixture\n")

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=forge", "GIT_AUTHOR_EMAIL=forge@local",
			"GIT_COMMITTER_NAME=forge", "GIT_COMMITTER_EMAIL=forge@local")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}
