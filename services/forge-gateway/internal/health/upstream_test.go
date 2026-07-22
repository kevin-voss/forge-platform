package health

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"forge.local/services/forge-gateway/internal/routes"
)

func TestThresholdStateMachine(t *testing.T) {
	tr := NewUpstreamTracker(UpstreamConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		TrustRuntimeStatus: false,
		ProbeInterval:      0,
	}, slog.Default())

	url := "http://127.0.0.1:18080"
	if !tr.IsReady(url) {
		t.Fatal("unknown upstream should default ready")
	}

	tr.RecordPassiveFailure(url)
	tr.RecordPassiveFailure(url)
	if !tr.IsReady(url) {
		t.Fatal("below failure threshold should stay ready")
	}
	tr.RecordPassiveFailure(url)
	if tr.IsReady(url) {
		t.Fatal("at failure threshold should be unready")
	}

	tr.RecordPassiveSuccess(url)
	if tr.IsReady(url) {
		t.Fatal("below success threshold should stay unready")
	}
	tr.RecordPassiveSuccess(url)
	if !tr.IsReady(url) {
		t.Fatal("at success threshold should be ready again")
	}
}

func boolPtr(v bool) *bool { return &v }

func TestSyncAuthoritativeWhenTrusted(t *testing.T) {
	tr := NewUpstreamTracker(UpstreamConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		TrustRuntimeStatus: true,
	}, slog.Default())

	url := "http://127.0.0.1:19090"
	tr.ApplySync([]SyncUpstream{{URL: url, Ready: boolPtr(false)}})
	if tr.IsReady(url) {
		t.Fatal("sync unready should mark unready")
	}
	tr.ApplySync([]SyncUpstream{{URL: url, Ready: boolPtr(true)}})
	if !tr.IsReady(url) {
		t.Fatal("sync ready should mark ready")
	}
}

func TestSyncIgnoredWhenNotTrusted(t *testing.T) {
	tr := NewUpstreamTracker(UpstreamConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		TrustRuntimeStatus: false,
	}, slog.Default())

	url := "http://127.0.0.1:19091"
	tr.ApplySync([]SyncUpstream{{URL: url, Ready: boolPtr(false)}})
	if !tr.IsReady(url) {
		t.Fatal("untrusted sync should not force unready")
	}
}

func TestSyncNilReadyDoesNotForce(t *testing.T) {
	tr := NewUpstreamTracker(UpstreamConfig{
		FailureThreshold:   1,
		SuccessThreshold:   1,
		TrustRuntimeStatus: true,
	}, slog.Default())
	url := "http://127.0.0.1:19092"
	tr.RecordPassiveFailure(url)
	if tr.IsReady(url) {
		t.Fatal("setup: should be unready")
	}
	tr.ApplySync([]SyncUpstream{{URL: url, Ready: nil}})
	if tr.IsReady(url) {
		t.Fatal("nil sync Ready should not override passive unready")
	}
}

func TestFilterReady(t *testing.T) {
	tr := NewUpstreamTracker(UpstreamConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
	}, slog.Default())
	a := "http://127.0.0.1:1"
	b := "http://127.0.0.1:2"
	tr.Reconcile([]string{a, b})
	tr.RecordPassiveFailure(a)

	got := tr.FilterReady([]routes.Upstream{{URL: a}, {URL: b}})
	if len(got) != 1 || got[0].URL != b {
		t.Fatalf("FilterReady=%+v, want only %s", got, b)
	}
}

func TestActiveProbeMarksReadyAndUnready(t *testing.T) {
	var ready bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			http.NotFound(w, r)
			return
		}
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"status":"not_ready"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	t.Cleanup(srv.Close)

	tr := NewUpstreamTracker(UpstreamConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		ProbePath:        "/health/ready",
		ProbeTimeout:     time.Second,
	}, slog.Default())

	url := srv.URL
	tr.Reconcile([]string{url})

	ready = false
	tr.RecordProbeFailure(url)
	if !tr.IsReady(url) {
		t.Fatal("one probe failure should stay ready")
	}
	tr.RecordProbeFailure(url)
	if tr.IsReady(url) {
		t.Fatal("probe failures should mark unready")
	}

	ready = true
	tr.RecordProbeSuccess(url)
	tr.RecordProbeSuccess(url)
	if !tr.IsReady(url) {
		t.Fatal("probe successes should re-add upstream")
	}

	// Exercise real HTTP probe helper.
	if !tr.probeOne(t.Context(), url) {
		t.Fatal("probeOne should succeed when ready")
	}
}

func TestReconcilePrunesRemoved(t *testing.T) {
	tr := NewUpstreamTracker(DefaultUpstreamConfig(), slog.Default())
	a := "http://127.0.0.1:1"
	b := "http://127.0.0.1:2"
	tr.Reconcile([]string{a, b})
	tr.Reconcile([]string{a})
	snap := tr.Snapshot()
	if _, ok := snap[normalizeUpstreamURL(b)]; ok {
		t.Fatalf("expected %s pruned, snap=%v", b, snap)
	}
	if _, ok := snap[normalizeUpstreamURL(a)]; !ok {
		t.Fatalf("expected %s kept, snap=%v", a, snap)
	}
}
