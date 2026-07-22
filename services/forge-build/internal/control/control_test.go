package control_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/control"
)

func TestIdempotencyKeyDerivation(t *testing.T) {
	if got := control.ImageIdempotencyKey("  abc  "); got != "build-abc" {
		t.Fatalf("ImageIdempotencyKey = %q", got)
	}
	if got := control.DeployIdempotencyKey("xyz"); got != "deploy-xyz" {
		t.Fatalf("DeployIdempotencyKey = %q", got)
	}
}

func TestRecordImageRequestShape(t *testing.T) {
	var gotPath, gotKey, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Idempotency-Key")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "sid-1",
			"image":        gotBody["image"],
			"imageDigest":  gotBody["digest"],
			"imageCommit":  gotBody["commit"],
			"imageBuildId": gotBody["buildId"],
		})
	}))
	defer srv.Close()

	c := control.New(srv.URL, srv.Client())
	out, err := c.RecordImage(context.Background(), "sid-1", control.RecordImageRequest{
		Image:   "localhost:5000/acme-api:abc-bid",
		Digest:  "sha256:dead",
		Commit:  "abc1234",
		BuildID: "bid-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/services/sid-1/image" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotKey != "build-bid-1" {
		t.Fatalf("idempotency key = %q", gotKey)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotBody["image"] != "localhost:5000/acme-api:abc-bid" {
		t.Fatalf("body = %#v", gotBody)
	}
	if out.Image != "localhost:5000/acme-api:abc-bid" || out.ImageBuildID != "bid-1" {
		t.Fatalf("out = %+v", out)
	}
}

func TestCreateDeploymentRequestShape(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Idempotency-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            "dep-1",
			"serviceId":     "sid-1",
			"environmentId": gotBody["environmentId"],
			"image":         gotBody["image"],
			"status":        "pending",
		})
	}))
	defer srv.Close()

	c := control.New(srv.URL, srv.Client())
	out, err := c.CreateDeployment(context.Background(), "sid-1", "bid-9", control.CreateDeploymentRequest{
		Image:         "localhost:5000/acme-api:abc-bid",
		EnvironmentID: "env-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/services/sid-1/deployments" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotKey != "deploy-bid-9" {
		t.Fatalf("key = %q", gotKey)
	}
	if out.ID != "dep-1" || out.Image != "localhost:5000/acme-api:abc-bid" {
		t.Fatalf("out = %+v", out)
	}
}

func TestTransientClassification(t *testing.T) {
	if !control.Transient(&control.HTTPError{StatusCode: 503}) {
		t.Fatal("503 should be transient")
	}
	if control.Transient(&control.HTTPError{StatusCode: 400}) {
		t.Fatal("400 should not be transient")
	}
	if !control.Transient(context.DeadlineExceeded) {
		t.Fatal("deadline should be transient")
	}
}

func TestRecordImageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"code":"unavailable"}}`)
	}))
	defer srv.Close()

	c := control.New(srv.URL, srv.Client())
	_, err := c.RecordImage(context.Background(), "sid", control.RecordImageRequest{
		Image: "localhost:5000/x:1", BuildID: "b1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !control.Transient(err) {
		t.Fatalf("expected transient, got %v", err)
	}
}

func TestDisabledClient(t *testing.T) {
	c := control.New("", nil)
	if c.Enabled() {
		t.Fatal("empty base URL should disable client")
	}
}

func TestRetriesSeeSameIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if calls.Load() == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sid", "image": "localhost:5000/x:1"})
	}))
	defer srv.Close()

	c := control.New(srv.URL, &http.Client{Timeout: 2 * time.Second})
	// Caller-level retry simulation.
	var err error
	for i := 0; i < 2; i++ {
		_, err = c.RecordImage(context.Background(), "sid", control.RecordImageRequest{
			Image: "localhost:5000/x:1", BuildID: "11111111-1111-4111-8111-111111111111",
		})
		if err == nil {
			break
		}
		if !control.Transient(err) {
			t.Fatalf("non-transient: %v", err)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	wantKey := "build-11111111-1111-4111-8111-111111111111"
	for _, k := range keys {
		if k != wantKey {
			t.Fatalf("keys=%v want %q", keys, wantKey)
		}
	}
}
