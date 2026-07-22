package registry_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"forge.local/services/forge-build/internal/logbuf"
	"forge.local/services/forge-build/internal/registry"
)

type fakeEngine struct {
	mu          sync.Mutex
	tagCalls    int
	pushCalls   int
	failPushes  int // fail this many push attempts before succeeding
	pushErr     error
	tagErr      error
	digest      string
	pushedRefs  []string
	taggedPairs [][2]string
}

func (f *fakeEngine) TagImage(_ context.Context, sourceRef, targetRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagCalls++
	f.taggedPairs = append(f.taggedPairs, [2]string{sourceRef, targetRef})
	return f.tagErr
}

func (f *fakeEngine) PushImage(_ context.Context, ref string, onLine func(string)) (string, error) {
	f.mu.Lock()
	f.pushCalls++
	call := f.pushCalls
	f.pushedRefs = append(f.pushedRefs, ref)
	failN := f.failPushes
	pushErr := f.pushErr
	digest := f.digest
	f.mu.Unlock()

	if onLine != nil {
		onLine("Pushing " + ref)
	}
	if call <= failN {
		err := pushErr
		if err == nil {
			err = errors.New("temporary registry error")
		}
		return "", err
	}
	if digest == "" {
		digest = "sha256:abcdef0123456789"
	}
	return digest, nil
}

func (f *fakeEngine) ImageDigest(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.digest != "" {
		return f.digest, nil
	}
	return "sha256:abcdef0123456789", nil
}

func TestPushRetriesSucceedOnNthTry(t *testing.T) {
	eng := &fakeEngine{failPushes: 2, digest: "sha256:retry-ok"}
	client := registry.New(eng, 3, nil)
	registry.SetBackoffForTest(client, 0)

	logs := logbuf.New(100)
	refs := registry.Refs{
		Versioned: "localhost:5000/api:abc1234-11111111",
		Latest:    "localhost:5000/api:latest",
	}
	digest, err := client.TagAndPush(context.Background(), "forge-build-local:bid", refs, logs)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:retry-ok" {
		t.Fatalf("digest = %q", digest)
	}
	// versioned: 3 attempts (2 fail + 1 ok); latest: 1 attempt
	if eng.pushCalls != 4 {
		t.Fatalf("pushCalls = %d, want 4", eng.pushCalls)
	}
	joined := strings.Join(logs.Snapshot(), "\n")
	if !strings.Contains(joined, "push retry") {
		t.Fatalf("logs missing retry: %s", joined)
	}
}

func TestPushRetriesExhausted(t *testing.T) {
	eng := &fakeEngine{failPushes: 100, pushErr: errors.New("registry down")}
	client := registry.New(eng, 2, nil)
	registry.SetBackoffForTest(client, 0)

	_, err := client.TagAndPush(context.Background(), "forge-build-local:bid", registry.Refs{
		Versioned: "localhost:5000/api:abc1234-11111111",
	}, logbuf.New(50))
	if err == nil || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("err = %v", err)
	}
	if eng.pushCalls != 3 {
		t.Fatalf("pushCalls = %d, want 3", eng.pushCalls)
	}
}

func TestTagFailureDoesNotPush(t *testing.T) {
	eng := &fakeEngine{tagErr: errors.New("tag denied")}
	client := registry.New(eng, 1, nil)
	_, err := client.TagAndPush(context.Background(), "forge-build-local:bid", registry.Refs{
		Versioned: "localhost:5000/api:abc1234-11111111",
	}, logbuf.New(50))
	if err == nil || !strings.Contains(err.Error(), "docker tag") {
		t.Fatalf("err = %v", err)
	}
	if eng.pushCalls != 0 {
		t.Fatalf("pushCalls = %d", eng.pushCalls)
	}
}

func TestStubPublisher(t *testing.T) {
	s := &registry.StubPublisher{Digest: "sha256:stubbed"}
	d, err := s.TagAndPush(context.Background(), "local:1", registry.Refs{Versioned: "localhost:5000/x:1"}, logbuf.New(10))
	if err != nil || d != "sha256:stubbed" || s.Calls != 1 {
		t.Fatalf("digest=%q err=%v calls=%d", d, err, s.Calls)
	}
}
