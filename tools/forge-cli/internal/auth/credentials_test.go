package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialStoreFileRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")
	store := OpenStoreAt(path, backendFile)

	if err := store.Put("local", Credentials{
		IdentityURL: "http://127.0.0.1:4002",
		Token:       "session-token-1",
	}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want 0600", info.Mode().Perm())
	}

	got, err := store.Get("local")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Token != "session-token-1" {
		t.Fatalf("token = %q, want session-token-1", got.Token)
	}
	if got.IdentityURL != "http://127.0.0.1:4002" {
		t.Fatalf("identity_url = %q", got.IdentityURL)
	}
	if got.Backend != backendFile {
		t.Fatalf("backend = %q, want file", got.Backend)
	}

	// Password must never be persisted — only the opaque token.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "s3cret!!") {
		t.Fatalf("credentials file unexpectedly contains password material: %s", raw)
	}

	if err := store.Delete("local"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credentials file still present after delete: %v", err)
	}
}

func TestCredentialStorePerProfileIsolation(t *testing.T) {
	dir := t.TempDir()
	store := OpenStoreAt(filepath.Join(dir, "credentials"), backendFile)
	if err := store.Put("dev", Credentials{Token: "dev-token"}); err != nil {
		t.Fatalf("Put(dev) error = %v", err)
	}
	if err := store.Put("ci", Credentials{Token: "ci-token"}); err != nil {
		t.Fatalf("Put(ci) error = %v", err)
	}
	dev, err := store.Get("dev")
	if err != nil || dev.Token != "dev-token" {
		t.Fatalf("Get(dev) = %#v, %v", dev, err)
	}
	ci, err := store.Get("ci")
	if err != nil || ci.Token != "ci-token" {
		t.Fatalf("Get(ci) = %#v, %v", ci, err)
	}
}

func TestResolveTokenPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	store := OpenStoreAt(filepath.Join(dir, "credentials"), backendFile)
	if err := store.Put("local", Credentials{Token: "stored"}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	t.Setenv("FORGE_TOKEN", "from-env")
	got, err := ResolveToken(store, "local")
	if err != nil {
		t.Fatalf("ResolveToken() error = %v", err)
	}
	if got != "from-env" {
		t.Fatalf("ResolveToken() = %q, want from-env", got)
	}
}
