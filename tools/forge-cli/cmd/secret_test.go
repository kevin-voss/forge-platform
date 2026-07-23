package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"forge.local/tools/forge-cli/internal/errmap"
)

func TestSecretListNeverIncludesValue(t *testing.T) {
	secretsServer := newSecretsFixture(t, map[string]any{
		"list": []map[string]any{
			{"name": "DATABASE_PASSWORD", "version": 2, "updated_at": "2026-07-23T01:00:00Z"},
		},
	})
	t.Setenv("FORGE_SECRETS_URL", secretsServer.URL)
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("CI", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := NewRootCommand("test")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--project", "prj_1", "--env", "production", "secret", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("secret list: %v", err)
	}
	out := stdout.String()
	if strings.Contains(out, "value") || strings.Contains(strings.ToLower(out), "s3cret") {
		t.Fatalf("table list leaked value: %q", out)
	}
	if !strings.Contains(out, "DATABASE_PASSWORD") || !strings.Contains(out, "2") {
		t.Fatalf("table list missing metadata: %q", out)
	}

	stdout.Reset()
	root = NewRootCommand("test")
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--project", "prj_1", "--env", "production", "secret", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("secret list --json: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		t.Fatalf("decode json: %v\n%s", err, stdout.String())
	}
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if _, hasValue := items[0]["value"]; hasValue {
		t.Fatalf("json list contains value field: %#v", items[0])
	}
	if items[0]["name"] != "DATABASE_PASSWORD" || int(items[0]["version"].(float64)) != 2 {
		t.Fatalf("json metadata = %#v", items[0])
	}
}

func TestSecretSetFromStdinProducesPutBody(t *testing.T) {
	var putBodies []string
	var putPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/secrets/") {
			body, _ := io.ReadAll(r.Body)
			putBodies = append(putBodies, string(body))
			putPaths = append(putPaths, r.URL.Path)
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"DATABASE_PASSWORD","version":` + strconv.Itoa(len(putBodies)) + `}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	t.Setenv("FORGE_SECRETS_URL", server.URL)
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("CI", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	runSet := func(stdin string) {
		t.Helper()
		root := NewRootCommand("test")
		var stdout bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(io.Discard)
		root.SetIn(strings.NewReader(stdin))
		root.SetArgs([]string{
			"--project", "prj_1", "--env", "production",
			"secret", "set", "DATABASE_PASSWORD", "--from-stdin",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("secret set: %v", err)
		}
	}

	runSet("pw1\n")
	runSet("pw1")
	if len(putBodies) != 2 {
		t.Fatalf("put count = %d", len(putBodies))
	}
	if putBodies[0] != `{"value":"pw1"}` || putBodies[1] != `{"value":"pw1"}` {
		t.Fatalf("put bodies = %#v", putBodies)
	}
	wantPath := "/v1/projects/prj_1/envs/production/secrets/DATABASE_PASSWORD"
	if putPaths[0] != wantPath {
		t.Fatalf("path = %q, want %q", putPaths[0], wantPath)
	}
}

func TestSecretSetFromFileAndRotate(t *testing.T) {
	versions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/secrets/DATABASE_PASSWORD"):
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Value string `json:"value"`
			}
			_ = json.Unmarshal(body, &payload)
			versions++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"DATABASE_PASSWORD","version":` + strconv.Itoa(versions) + `}`))
			if versions == 1 && payload.Value != "from-file" {
				t.Errorf("set value = %q", payload.Value)
			}
			if versions == 2 && payload.Value != "pw2" {
				t.Errorf("rotate value = %q", payload.Value)
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secrets"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"name":"DATABASE_PASSWORD","version":2,"updated_at":"t"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	t.Setenv("FORGE_SECRETS_URL", server.URL)
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("CI", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	file := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCommand("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"--project", "prj_1", "--env", "production",
		"secret", "set", "DATABASE_PASSWORD", "--from-file", file,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("set from file: %v", err)
	}

	root = NewRootCommand("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader("pw2"))
	root.SetArgs([]string{
		"--project", "prj_1", "--env", "production",
		"secret", "rotate", "DATABASE_PASSWORD", "--from-stdin",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if versions != 2 {
		t.Fatalf("versions = %d, want 2", versions)
	}
}

func TestSecretAndConfigExitCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-sec-1")
		switch {
		case strings.Contains(r.URL.Path, "/secrets") && r.Header.Get("Authorization") == "Bearer bad":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing Authorization bearer token","code":"unauthenticated"}`))
		case strings.Contains(r.URL.Path, "/projects/prj_other/"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"project isolation denied","code":"forbidden"}`))
		case strings.HasSuffix(r.URL.Path, "/secrets/MISSING"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"secret not found","code":"not_found"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)

	t.Setenv("FORGE_SECRETS_URL", server.URL)
	t.Setenv("CI", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	run := func(token string, args ...string) error {
		t.Helper()
		t.Setenv("FORGE_TOKEN", token)
		root := NewRootCommand("test")
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(args)
		return root.Execute()
	}

	err401 := run("bad", "--project", "prj_1", "--env", "production", "secret", "list")
	if errmap.ExitCode(err401) != errmap.Auth {
		t.Fatalf("401 exit = %d (%v)", errmap.ExitCode(err401), err401)
	}
	if !strings.Contains(err401.Error(), "forge login") {
		t.Fatalf("401 message = %q", err401.Error())
	}
	if !strings.Contains(err401.Error(), "req-sec-1") {
		t.Fatalf("401 missing requestId: %q", err401.Error())
	}

	err403 := run("tok", "--project", "prj_other", "--env", "production", "secret", "list")
	if errmap.ExitCode(err403) != errmap.Auth {
		t.Fatalf("403 exit = %d (%v)", errmap.ExitCode(err403), err403)
	}
	if !strings.Contains(err403.Error(), "project isolation") {
		t.Fatalf("403 message = %q", err403.Error())
	}

	root := NewRootCommand("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader("x"))
	t.Setenv("FORGE_TOKEN", "tok")
	root.SetArgs([]string{"--project", "prj_1", "--env", "production", "secret", "rotate", "MISSING", "--from-stdin"})
	err404 := root.Execute()
	if errmap.ExitCode(err404) != errmap.NotFound {
		t.Fatalf("404 exit = %d (%v)", errmap.ExitCode(err404), err404)
	}
}

func TestConfigSetAndShow(t *testing.T) {
	store := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/config/"):
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Value string `json:"value"`
			}
			_ = json.Unmarshal(body, &payload)
			name := filepath.Base(r.URL.Path)
			store[name] = payload.Value
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"name": name, "value": payload.Value, "updated_at": "t",
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/config"):
			items := make([]map[string]string, 0, len(store))
			for name, value := range store {
				items = append(items, map[string]string{"name": name, "value": value, "updated_at": "t"})
			}
			_ = json.NewEncoder(w).Encode(items)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	t.Setenv("FORGE_SECRETS_URL", server.URL)
	t.Setenv("FORGE_TOKEN", "tok")
	t.Setenv("CI", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := NewRootCommand("test")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--project", "prj_1", "--env", "production", "config", "set", "FEATURE_X=true"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if !strings.Contains(stdout.String(), "FEATURE_X=true") {
		t.Fatalf("set output = %q", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand("test")
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--project", "prj_1", "--env", "production", "config", "show"})
	if err := root.Execute(); err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(stdout.String(), "FEATURE_X") || !strings.Contains(stdout.String(), "true") {
		t.Fatalf("show output = %q", stdout.String())
	}
}

func TestCLIProfileConfigStillWorks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	root := NewRootCommand("test")
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"config", "set", "endpoint", "http://127.0.0.1:4001", "--profile", "local"})
	if err := root.Execute(); err != nil {
		t.Fatalf("profile config set: %v", err)
	}
	if !strings.Contains(stdout.String(), `profile "local"`) {
		t.Fatalf("output = %q", stdout.String())
	}
}

func newSecretsFixture(t *testing.T, data map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secrets") {
			_ = json.NewEncoder(w).Encode(data["list"])
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}
