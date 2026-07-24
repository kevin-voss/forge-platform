package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Repository-layer CRUD against a real Postgres test container (or SNAPNOTE_TEST_DATABASE_URL).
func TestNoteStorePostgresCRUD(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SNAPNOTE_TEST_DATABASE_URL"))
	var cleanup func()
	if dsn == "" {
		var err error
		dsn, cleanup, err = startPostgresContainer(t)
		if err != nil {
			t.Skipf("postgres test container unavailable: %v", err)
		}
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store, err := openStore(dsn, resolveMigrationsDir(""))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	created, err := store.CreateNote(ctx, "Trip photos", "Lake day")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Title != "Trip photos" || created.Body != "Lake day" {
		t.Fatalf("unexpected create: %+v", created)
	}

	got, err := store.GetNote(ctx, created.ID)
	if err != nil || got == nil || got.Title != "Trip photos" {
		t.Fatalf("get: got=%+v err=%v", got, err)
	}

	body := "Updated body"
	patched, err := store.PatchNote(ctx, created.ID, nil, &body)
	if err != nil || patched == nil || patched.Body != "Updated body" {
		t.Fatalf("patch: got=%+v err=%v", patched, err)
	}

	listed, err := store.ListNotes(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list: %+v err=%v", listed, err)
	}

	atts, err := store.ListAttachments(ctx, created.ID)
	if err != nil || len(atts) != 0 {
		t.Fatalf("attachments stub: %+v err=%v", atts, err)
	}

	if err := store.DeleteNote(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := store.GetNote(ctx, created.ID)
	if err != nil || gone != nil {
		t.Fatalf("after delete: got=%+v err=%v", gone, err)
	}
}

func startPostgresContainer(t *testing.T) (string, func(), error) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return "", nil, fmt.Errorf("docker not on PATH")
	}
	name := fmt.Sprintf("snapnote-pg-test-%d", time.Now().UnixNano())
	run := exec.Command(
		"docker", "run", "-d", "--rm",
		"--name", name,
		"-e", "POSTGRES_PASSWORD=test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_DB=snapnote",
		"-p", "127.0.0.1::5432",
		"postgres:16-alpine",
	)
	out, err := run.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	id := strings.TrimSpace(string(out))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", id).Run()
	}

	portOut, err := exec.Command("docker", "port", id, "5432/tcp").CombinedOutput()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker port: %w (%s)", err, strings.TrimSpace(string(portOut)))
	}
	line := strings.TrimSpace(string(portOut))
	hostPort := line[strings.LastIndex(line, ":")+1:]
	dsn := fmt.Sprintf("postgres://test:test@127.0.0.1:%s/snapnote?sslmode=disable", hostPort)

	deadline := time.Now().Add(45 * time.Second)
	for {
		store, err := openStore(dsn, resolveMigrationsDir(""))
		if err == nil {
			_ = store.Close()
			return dsn, cleanup, nil
		}
		if time.Now().After(deadline) {
			cleanup()
			return "", nil, fmt.Errorf("postgres not ready: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
