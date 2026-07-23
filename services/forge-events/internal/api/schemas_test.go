package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"forge.local/services/forge-events/internal/api"
	"forge.local/services/forge-events/internal/schema"
)

type stubSchemaRegistry struct {
	list map[string]schema.SubjectInfo
	get  schema.SubjectDetail
	err  error
}

func (s *stubSchemaRegistry) List() map[string]schema.SubjectInfo { return s.list }

func (s *stubSchemaRegistry) Get(string) (schema.SubjectDetail, error) {
	return s.get, s.err
}

func TestSchemasHandlerList(t *testing.T) {
	h := &api.SchemasHandler{Registry: &stubSchemaRegistry{
		list: map[string]schema.SubjectInfo{
			"application.crashed": {Versions: []int{1}, LatestVersion: 1},
		},
	}}
	mux := http.NewServeMux()
	h.Register(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/schemas", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]schema.SubjectInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["application.crashed"].LatestVersion != 1 {
		t.Fatalf("got %#v", got)
	}
}

func TestSchemasHandlerGetNotFound(t *testing.T) {
	h := &api.SchemasHandler{Registry: &stubSchemaRegistry{err: schema.ErrUnknownSchema}}
	mux := http.NewServeMux()
	h.Register(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/schemas/nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestPublishHandlerSchemaValidation422(t *testing.T) {
	pub := &stubPublisher{err: &schema.Error{
		Subject: "application.crashed",
		Reason:  "validation_failed",
		Violations: []schema.Violation{
			{Path: "service", Message: "missing property 'service'", Keyword: "required"},
		},
	}}
	h := &api.PublishHandler{Publisher: pub, MaxBytes: 1024}
	mux := http.NewServeMux()
	h.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/events",
		bytes.NewBufferString(`{"subject":"application.crashed","data":{"reason":"oom"},"source":"runtime"}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["error"] != "validation_failed" || got["subject"] != "application.crashed" {
		t.Fatalf("body = %#v", got)
	}
	violations, ok := got["violations"].([]any)
	if !ok || len(violations) == 0 {
		t.Fatalf("violations = %#v", got["violations"])
	}
}
