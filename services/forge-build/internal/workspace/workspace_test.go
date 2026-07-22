package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAndCleanup(t *testing.T) {
	root := t.TempDir()
	mgr, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if mgr.Root() != filepath.Clean(root) {
		t.Fatalf("Root = %q, want %q", mgr.Root(), filepath.Clean(root))
	}

	dir, err := mgr.Create("build-abc")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := filepath.Join(root, "build-abc")
	if dir != want {
		t.Fatalf("Create dir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("perm = %o, want 0700", info.Mode().Perm())
	}

	nested := filepath.Join(dir, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(nested, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := mgr.Cleanup("build-abc"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected workspace removed, got err=%v", err)
	}
}

func TestNewRequiresWritableDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := New(root); err == nil {
		t.Fatal("expected error for missing workspace root")
	}
}

func TestCreateRejectsBadBuildID(t *testing.T) {
	mgr, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, id := range []string{"", "../escape", "a/b", `a\b`} {
		if _, err := mgr.Create(id); err == nil {
			t.Fatalf("Create(%q) expected error", id)
		}
	}
}
