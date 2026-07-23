package api_test

import (
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

type stubQuerier struct {
	values []logs.StreamValue
	err    error
}

func (s *stubQuerier) QueryRange(_ context.Context, _ string, _, _ time.Time, _ int, _ string) ([]logs.StreamValue, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.values, nil
}

type stubIdentity struct {
	principals map[string]identity.Principal
	allow      map[string]bool
}

func (s *stubIdentity) Introspect(_ context.Context, token string) (identity.Principal, error) {
	p, ok := s.principals[token]
	if !ok {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	return p, nil
}

func (s *stubIdentity) Check(_ context.Context, _ identity.Principal, projectID, _ string) (bool, error) {
	if s.allow == nil {
		return true, nil
	}
	return s.allow[projectID], nil
}

func TestLogsBareQueryRejected(t *testing.T) {
	h := &api.LogsHandler{
		Service: &logs.Service{Loki: &stubQuerier{}, Caps: logs.DefaultCaps()},
		Caps:    logs.DefaultCaps(),
		Auth:    &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?limit=10", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestLogsQueryOK(t *testing.T) {
	ts := time.Now().UTC()
	line, _ := json.Marshal(map[string]any{
		"message": "hello", "service": "control", "trace_id": "T1",
		"level": "info", "forge.deployment": "dpl_1", "forge.project": "prj_1",
	})
	h := &api.LogsHandler{
		Service: &logs.Service{Loki: &stubQuerier{values: []logs.StreamValue{{
			Timestamp: ts, Line: string(line),
			Labels: map[string]string{"forge_project": "prj_1", "forge_service": "control"},
		}}}, Caps: logs.DefaultCaps()},
		Caps: logs.DefaultCaps(),
		Auth: &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?project=prj_1&deployment=dpl_1&limit=20", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var body logs.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Entries) != 1 || body.Entries[0].TraceID != "T1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestLogsLokiUnavailable(t *testing.T) {
	h := &api.LogsHandler{
		Service: &logs.Service{Loki: &stubQuerier{err: context.DeadlineExceeded}, Caps: logs.DefaultCaps()},
		Caps:    logs.DefaultCaps(),
		Auth:    &identity.Gate{Mode: identity.AuthModeDev},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?trace_id=T", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "loki_unavailable") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestLogsEnforceDeniesOtherProject(t *testing.T) {
	id := &stubIdentity{
		principals: map[string]identity.Principal{
			"tok": {
				ID: "usr_1", Type: "user",
				AllowedProjs: map[string]struct{}{"prj_ok": {}},
			},
		},
		allow: map[string]bool{"prj_ok": true, "prj_other": false},
	}
	h := &api.LogsHandler{
		Service: &logs.Service{Loki: &stubQuerier{}, Caps: logs.DefaultCaps()},
		Caps:    logs.DefaultCaps(),
		Auth:    &identity.Gate{Mode: identity.AuthModeEnforce, Client: id, Action: "project.read"},
	}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/v1/logs?project=prj_other", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}
