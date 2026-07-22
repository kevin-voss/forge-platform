package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/store"
)

func TestPutGetListSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	finished := now.Add(time.Minute)
	rec := store.Record{
		ID:         "11111111-1111-4111-8111-111111111111",
		Repo:       "file:///tmp/app",
		Ref:        "main",
		Status:     "succeeded",
		Phase:      "succeeded",
		Image:      "localhost:5000/api:abc-11111111",
		Digest:     "sha256:deadbeef",
		Commit:     "deadbeef",
		StartedAt:  now,
		FinishedAt: &finished,
	}
	if err := st.Put(rec); err != nil {
		t.Fatal(err)
	}

	st2, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := st2.Get(rec.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Image != rec.Image || got.Status != "succeeded" || got.Digest != rec.Digest {
		t.Fatalf("got=%+v", got)
	}
	list, err := st2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len=%d", len(list))
	}
}

func TestPutClearsImageOnFailure(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = st.Put(store.Record{
		ID:        "22222222-2222-4222-8222-222222222222",
		Repo:      "file:///x",
		Ref:       "main",
		Status:    "failed",
		Phase:     "failed",
		Image:     "should-not-persist",
		Digest:    "sha256:nope",
		StartedAt: time.Now().UTC(),
		Error:     &store.ErrorInfo{Code: "build_failed", Message: "boom"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Get("22222222-2222-4222-8222-222222222222")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.Image != "" || got.Digest != "" {
		t.Fatalf("image=%q digest=%q", got.Image, got.Digest)
	}
	if got.Error == nil || got.Error.Code != "build_failed" {
		t.Fatalf("error=%+v", got.Error)
	}
}

func TestStoreDirMustBeAbsolute(t *testing.T) {
	if _, err := store.New("relative"); err == nil {
		t.Fatal("expected error")
	}
	abs := filepath.Join(t.TempDir(), "nested")
	if _, err := store.New(abs); err != nil {
		t.Fatal(err)
	}
}
