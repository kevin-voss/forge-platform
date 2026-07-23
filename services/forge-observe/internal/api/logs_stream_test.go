package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-observe/internal/api"
	"forge.local/services/forge-observe/internal/identity"
	"forge.local/services/forge-observe/internal/logs"
)

func TestLogsStreamSSEShape(t *testing.T) {
	ts := time.Date(2026, 7, 23, 12, 0, 0, 1, time.UTC)
	line, _ := json.Marshal(map[string]any{
		"message": "live", "service": "demo", "trace_id": "T9",
		"level": "info", "forge.deployment": "dpl_1", "forge.project": "prj_1",
	})
	h := &api.LogsStreamHandler{
		Service: &logs.Service{
			Loki: &stubQuerier{values: []logs.StreamValue{{
				Timestamp: ts, Line: string(line),
				Labels: map[string]string{"forge_project": "prj_1", "forge_service": "demo"},
			}}},
			Now: func() time.Time { return ts.Add(time.Second) },
		},
		Caps: logs.DefaultCaps(),
		Auth: &identity.Gate{Mode: identity.AuthModeDev},
		Opts: logs.TailOptions{PollInterval: 50 * time.Millisecond, BatchLimit: 10},
		Now:  func() time.Time { return ts.Add(time.Second) },
	}
	mux := http.NewServeMux()
	h.Register(mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/logs/stream?project=prj_1&deployment=dpl_1&since="+ts.Add(-time.Minute).Format(time.RFC3339Nano), nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(rr, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		body = rr.Body.String()
		if strings.Contains(body, "event: log") && strings.Contains(body, `"message":"live"`) {
			cancel()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	<-done

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(body, "event: log") {
		t.Fatalf("missing event: log in %q", body)
	}
	var entry logs.Entry
	found := false
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &entry); err == nil && entry.Message == "live" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("no LogEntry in SSE body: %q", body)
	}
	if entry.TraceID != "T9" || entry.Service != "demo" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestLogsStreamLokiUnavailable(t *testing.T) {
	h := &api.LogsStreamHandler{
		Service: &logs.Service{Loki: &stubQuerier{err: context.DeadlineExceeded}},
		Caps:    logs.DefaultCaps(),
		Auth:    &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/stream?trace_id=T", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "loki_unavailable") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestLogsStreamRejectsBareQuery(t *testing.T) {
	h := &api.LogsStreamHandler{
		Service: &logs.Service{Loki: &stubQuerier{}},
		Caps:    logs.DefaultCaps(),
		Auth:    &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/logs/stream", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rr.Code)
	}
}
