package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/api"
	"forge.local/services/forge-build/internal/builder"
	"forge.local/services/forge-build/internal/jobs"
	"forge.local/services/forge-build/internal/logbuf"
)

func TestListAndCancelHTTP(t *testing.T) {
	wsRoot := t.TempDir()
	mgr, cleanup := newTestManager(t, wsRoot, instantBuilder{}, 5*time.Second)
	defer cleanup()

	mux := http.NewServeMux()
	api.NewBuildHandler(mgr, "forge.yaml").Register(mux)
	api.NewStatusHandler(mgr).Register(mux)

	repo := initAPIFixtureRepo(t)
	body := `{"repo":"` + repo + `","ref":"main"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/builds", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("create=%d body=%s", rr.Code, rr.Body.String())
	}
	var accepted api.BuildAccepted
	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	rec := waitBuildViaManager(t, mgr, accepted.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusSucceeded {
		t.Fatalf("status=%s", rec.Status)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/builds?status=succeeded", nil)
	listRR := httptest.NewRecorder()
	mux.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list=%d", listRR.Code)
	}
	var list []api.BuildRecord
	if err := json.Unmarshal(listRR.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].BuildID != accepted.BuildID {
		t.Fatalf("list=%+v", list)
	}
	if !api.EnforceImageInvariant(list[0]) {
		t.Fatalf("invariant: %+v", list[0])
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/builds/"+accepted.BuildID+"/cancel", nil)
	cancelRR := httptest.NewRecorder()
	mux.ServeHTTP(cancelRR, cancelReq)
	if cancelRR.Code != http.StatusConflict {
		t.Fatalf("cancel terminal want 409 got %d body=%s", cancelRR.Code, cancelRR.Body.String())
	}
}

func TestCancelRunningHTTP(t *testing.T) {
	wsRoot := t.TempDir()
	mgr, cleanup := newTestManager(t, wsRoot, &slowBuilder{delay: 3 * time.Second}, 10*time.Second)
	defer cleanup()

	mux := http.NewServeMux()
	api.NewBuildHandler(mgr, "forge.yaml").Register(mux)
	api.NewStatusHandler(mgr).Register(mux)

	repo := initAPIFixtureRepo(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/builds", strings.NewReader(`{"repo":"`+repo+`","ref":"main"}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var accepted api.BuildAccepted
	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		rec, _ := mgr.Get(accepted.BuildID)
		if rec.Status == jobs.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never running: %s", rec.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/builds/"+accepted.BuildID+"/cancel", nil)
	cancelRR := httptest.NewRecorder()
	mux.ServeHTTP(cancelRR, cancelReq)
	if cancelRR.Code != http.StatusAccepted {
		t.Fatalf("cancel=%d body=%s", cancelRR.Code, cancelRR.Body.String())
	}
	var cancel api.CancelAccepted
	if err := json.Unmarshal(cancelRR.Body.Bytes(), &cancel); err != nil {
		t.Fatal(err)
	}
	if cancel.Status != "canceling" {
		t.Fatalf("cancel=%+v", cancel)
	}
	rec := waitBuildViaManager(t, mgr, accepted.BuildID, 5*time.Second)
	if rec.Status != jobs.StatusCanceled {
		t.Fatalf("status=%s", rec.Status)
	}
}

type slowBuilder struct{ delay time.Duration }

func (s *slowBuilder) Build(ctx context.Context, _ builder.Options, logs *logbuf.Buffer) error {
	logs.Append("slow-build-start")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.delay):
		logs.Append("slow-build-done")
		return nil
	}
}

var _ builder.ImageBuilder = (*slowBuilder)(nil)
