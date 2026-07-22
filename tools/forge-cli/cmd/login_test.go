package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/auth"
	"forge.local/tools/forge-cli/internal/errmap"
)

func TestLoginTokenWhoamiLogoutAndControlBearer(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("FORGE_CREDENTIALS_BACKEND", "file")
	t.Setenv("CI", "1")
	t.Setenv("FORGE_TOKEN", "")

	var identityCalls []string
	var controlAuth string
	loggedOut := false

	identity := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		identityCalls = append(identityCalls, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/auth/introspect":
			body, _ := io.ReadAll(request.Body)
			var payload struct {
				Token string `json:"token"`
			}
			_ = json.Unmarshal(body, &payload)
			if payload.Token == "revoked" || loggedOut {
				_, _ = writer.Write([]byte(`{"active":false}`))
				return
			}
			_, _ = writer.Write([]byte(`{
				"active":true,
				"principal_type":"user",
				"principal_id":"usr_1",
				"user_id":"usr_1",
				"project_id":"prj_1",
				"role":"developer"
			}`))
		case "/v1/auth/logout":
			if got := request.Header.Get("Authorization"); got != "Bearer pat-token" {
				t.Errorf("logout Authorization = %q", got)
			}
			loggedOut = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected identity path %s", request.URL.Path)
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer identity.Close()

	control := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		controlAuth = request.Header.Get("Authorization")
		if controlAuth == "" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":{"code":"unauthenticated","message":"missing Authorization bearer token","requestId":"r1"}}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"id":"project-1","name":"acme","slug":"acme","createdAt":"now","updatedAt":"now"}]`))
	}))
	defer control.Close()

	t.Setenv("FORGE_IDENTITY_URL", identity.URL)

	run := func(args ...string) (string, error) {
		t.Helper()
		root := NewRootCommand("test")
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs(append(args, "--endpoint", control.URL, "--profile", "local"))
		err := root.Execute()
		return stdout.String() + stderr.String(), err
	}

	if output, err := run("login", "--token", "pat-token"); err != nil || !strings.Contains(output, `logged in to profile "local"`) {
		t.Fatalf("login: output=%q err=%v", output, err)
	}

	credsPath := filepath.Join(configHome, "forge", "credentials")
	info, err := os.Stat(credsPath)
	if err != nil {
		t.Fatalf("credentials missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %o, want 0600", info.Mode().Perm())
	}

	if output, err := run("whoami"); err != nil || !strings.Contains(output, "usr_1") || !strings.Contains(output, "developer") {
		t.Fatalf("whoami: output=%q err=%v", output, err)
	}

	if output, err := run("project", "list"); err != nil || !strings.Contains(output, "acme") {
		t.Fatalf("project list: output=%q err=%v auth=%q", output, err, controlAuth)
	}
	if controlAuth != "Bearer pat-token" {
		t.Fatalf("Control Authorization = %q, want Bearer pat-token", controlAuth)
	}

	if output, err := run("logout"); err != nil || !strings.Contains(output, `logged out of profile "local"`) {
		t.Fatalf("logout: output=%q err=%v", output, err)
	}
	if !loggedOut {
		t.Fatal("expected Identity logout call")
	}
	if _, err := os.Stat(credsPath); !os.IsNotExist(err) {
		t.Fatalf("credentials still present after logout: %v", err)
	}

	_, err = run("project", "list")
	if err == nil {
		t.Fatal("expected auth error after logout")
	}
	if code := errmap.ExitCode(err); code != errmap.Auth {
		t.Fatalf("post-logout exit = %d, want %d (%v)", code, errmap.Auth, err)
	}
	if !strings.Contains(err.Error(), "forge login") {
		t.Fatalf("post-logout error = %v", err)
	}

	// Non-interactive FORGE_TOKEN path.
	t.Setenv("FORGE_TOKEN", "pat-token")
	loggedOut = false
	if output, err := run("login"); err != nil || !strings.Contains(output, `logged in`) {
		t.Fatalf("login via FORGE_TOKEN: output=%q err=%v", output, err)
	}
	t.Setenv("FORGE_TOKEN", "")

	_ = identityCalls
}

func TestLoginRejectsInactiveToken(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("FORGE_CREDENTIALS_BACKEND", "file")
	t.Setenv("CI", "1")

	identity := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"active":false}`))
	}))
	defer identity.Close()
	t.Setenv("FORGE_IDENTITY_URL", identity.URL)

	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"login", "--token", "bad", "--endpoint", "http://127.0.0.1:4001"})
	err := root.Execute()
	var authErr *auth.Error
	if !errors.As(err, &authErr) || !strings.Contains(authErr.Error(), "inactive") {
		t.Fatalf("error = %v, want inactive auth error", err)
	}
	if errmap.ExitCode(err) != errmap.Auth {
		t.Fatalf("exit = %d, want %d", errmap.ExitCode(err), errmap.Auth)
	}
}
