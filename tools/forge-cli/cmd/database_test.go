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

	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/errmap"
)

func TestDatabaseCreateAttachListBackupRestoreRotateDelete(t *testing.T) {
	projectID := "11111111-1111-1111-1111-111111111111"
	instanceID := "22222222-2222-2222-2222-222222222222"
	databaseID := "33333333-3333-3333-3333-333333333333"
	appID := "44444444-4444-4444-4444-444444444444"
	attachmentID := "55555555-5555-5555-5555-555555555555"
	backupID := "66666666-6666-6666-6666-666666666666"

	var (
		createdInstance bool
		createdDatabase bool
		attached        bool
		backedUp        bool
		restored        bool
		rotated         bool
		deleted         bool
		detached        bool
		patched         bool
		gotProjectHeader string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/instances":
			createdInstance = true
			if r.Header.Get("X-Forge-Project") != projectID {
				t.Errorf("create instance X-Forge-Project = %q", r.Header.Get("X-Forge-Project"))
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{
				"id":"` + instanceID + `",
				"projectId":"` + projectID + `",
				"name":"main",
				"status":"available",
				"engine":"postgres",
				"deletionProtection":true,
				"host":"127.0.0.1",
				"port":5433,
				"createdAt":"now",
				"updatedAt":"now"
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/instances/"+instanceID+"/databases":
			createdDatabase = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{
				"id":"` + databaseID + `",
				"instanceId":"` + instanceID + `",
				"name":"main",
				"status":"available",
				"deletionProtection":true,
				"host":"127.0.0.1",
				"port":5433,
				"secretRef":"secret:project/` + projectID + `/env/managed-db/name/managed-db-` + databaseID + `",
				"username":"main_user",
				"createdAt":"now"
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/databases/instances":
			gotProjectHeader = r.Header.Get("X-Forge-Project")
			_, _ = w.Write([]byte(`[{
				"id":"` + instanceID + `",
				"projectId":"` + projectID + `",
				"name":"main",
				"status":"available",
				"engine":"postgres",
				"deletionProtection":true,
				"host":"127.0.0.1",
				"port":5433,
				"createdAt":"now",
				"updatedAt":"now"
			}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/databases/instances/"+instanceID+"/databases":
			_, _ = w.Write([]byte(`[{
				"id":"` + databaseID + `",
				"instanceId":"` + instanceID + `",
				"name":"main",
				"status":"available",
				"deletionProtection":true,
				"host":"127.0.0.1",
				"port":5433,
				"secretRef":"secret:ref",
				"createdAt":"now"
			}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/"+projectID+"/applications":
			_, _ = w.Write([]byte(`[{"id":"` + appID + `","projectId":"` + projectID + `","name":"backend","createdAt":"now","updatedAt":"now"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/"+databaseID+"/attach":
			attached = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), appID) || !strings.Contains(string(body), "DATABASE_URL") {
				t.Errorf("attach body = %s", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{
				"id":"` + attachmentID + `",
				"databaseId":"` + databaseID + `",
				"applicationId":"` + appID + `",
				"envVar":"DATABASE_URL",
				"secretRef":"secret:url",
				"createdAt":"now"
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/"+databaseID+"/backups":
			backedUp = true
			if r.Header.Get("X-Forge-Project") != projectID {
				t.Errorf("backup X-Forge-Project = %q", r.Header.Get("X-Forge-Project"))
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"` + backupID + `","databaseId":"` + databaseID + `","status":"running","createdAt":"now"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/databases/"+databaseID+"/backups/"+backupID:
			_, _ = w.Write([]byte(`{"id":"` + backupID + `","databaseId":"` + databaseID + `","status":"succeeded","checksum":"abc","createdAt":"now","restoreStatus":"succeeded"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/backups/"+backupID+"/restore":
			restored = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"backupId":"` + backupID + `","targetDatabaseId":"` + databaseID + `","status":"running"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/"+databaseID+"/rotate-credentials":
			rotated = true
			_, _ = w.Write([]byte(`{
				"credential":{"id":"77777777-7777-7777-7777-777777777777","username":"main_rot","status":"active","secretRef":"secret:new","createdAt":"now","rotatedAt":"now"},
				"secretRef":"secret:new"
			}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/databases/"+databaseID:
			patched = true
			_, _ = w.Write([]byte(`{
				"id":"` + databaseID + `",
				"instanceId":"` + instanceID + `",
				"name":"main",
				"status":"available",
				"deletionProtection":false,
				"createdAt":"now"
			}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/databases/"+databaseID:
			deleted = true
			if r.URL.Query().Get("force") != "true" {
				t.Errorf("delete force = %q", r.URL.Query().Get("force"))
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/databases/attachments/"+attachmentID:
			detached = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("CI", "1")
	t.Setenv("FORGE_TOKEN", "tok")
	writeDemoProfile(t, cfgHome, server.URL)

	run := func(args ...string) (string, error) {
		t.Helper()
		root := NewRootCommand("test")
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"--project", projectID, "--endpoint", server.URL}, args...))
		err := root.Execute()
		return out.String(), err
	}

	if out, err := run("database", "create", "main"); err != nil || !strings.Contains(out, databaseID) {
		t.Fatalf("create: out=%q err=%v", out, err)
	}
	if !createdInstance || !createdDatabase {
		t.Fatalf("create flags instance=%v database=%v", createdInstance, createdDatabase)
	}

	if out, err := run("database", "attach", "main", "--app", "backend"); err != nil || !strings.Contains(out, attachmentID) {
		t.Fatalf("attach: out=%q err=%v", out, err)
	}
	if !attached {
		t.Fatal("attach not called")
	}

	if out, err := run("--output", "json", "database", "list"); err != nil {
		t.Fatalf("list: %v", err)
	} else {
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("list json: %v\n%s", err, out)
		}
		if len(rows) != 1 || rows[0]["name"] != "main" {
			t.Fatalf("list rows = %#v", rows)
		}
		if gotProjectHeader != projectID {
			t.Fatalf("list project header = %q", gotProjectHeader)
		}
	}

	if out, err := run("--output", "json", "database", "backup", "main", "--wait=false"); err != nil || !strings.Contains(out, backupID) {
		t.Fatalf("backup: out=%q err=%v", out, err)
	}
	if !backedUp {
		t.Fatal("backup not called")
	}

	if out, err := run("database", "restore", backupID, "--target", "main", "--wait=false"); err != nil || !strings.Contains(out, "running") {
		t.Fatalf("restore: out=%q err=%v", out, err)
	}
	if !restored {
		t.Fatal("restore not called")
	}

	if out, err := run("database", "rotate", "main"); err != nil || !strings.Contains(out, "main_rot") {
		t.Fatalf("rotate: out=%q err=%v", out, err)
	}
	if !rotated {
		t.Fatal("rotate not called")
	}

	if out, err := run("database", "detach", attachmentID); err != nil || !strings.Contains(out, "detached") {
		t.Fatalf("detach: out=%q err=%v", out, err)
	}
	if !detached {
		t.Fatal("detach not called")
	}

	if out, err := run("database", "delete", "main", "--force"); err != nil || !strings.Contains(out, "deleted") {
		t.Fatalf("delete: out=%q err=%v", out, err)
	}
	if !patched || !deleted {
		t.Fatalf("delete patched=%v deleted=%v", patched, deleted)
	}
}

func TestDatabaseCreateRequiresProjectAndValidName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CI", "1")
	t.Setenv("FORGE_PROJECT", "")

	root := NewRootCommand("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--endpoint", "http://127.0.0.1:9", "database", "create", "main"})
	err := root.Execute()
	var usage *config.UsageError
	if !errors.As(err, &usage) || !strings.Contains(usage.Message, "--project") {
		t.Fatalf("err = %v", err)
	}
	if code := errmap.ExitCode(err); code != errmap.Usage {
		t.Fatalf("exit = %d", code)
	}

	root = NewRootCommand("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--project", "p1", "--endpoint", "http://127.0.0.1:9", "database", "create", "Bad-Name"})
	err = root.Execute()
	if !errors.As(err, &usage) || !strings.Contains(usage.Message, "database name must match") {
		t.Fatalf("err = %v", err)
	}
}

func writeDemoProfile(t *testing.T, cfgHome, endpoint string) {
	t.Helper()
	dir := filepath.Join(cfgHome, "forge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "current_profile: demo\nprofiles:\n  demo:\n    endpoint: " + endpoint + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
