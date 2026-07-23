package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthReady(t *testing.T) {
	srv := newServer()
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %q, want ok", body["status"])
	}
}

func TestTasksCRUDStub(t *testing.T) {
	srv := newServer()
	handler := srv.routes()

	createBody := bytes.NewBufferString(`{"title":"Buy milk"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/tasks", createBody)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRec.Code, createRec.Body.String())
	}
	var created Task
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Title != "Buy milk" || created.Done {
		t.Fatalf("unexpected created task: %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRec.Code)
	}
	var listed []*Task
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list = %+v, want one task %s", listed, created.ID)
	}

	patchBody := bytes.NewBufferString(`{"done":true}`)
	patchReq := httptest.NewRequest(http.MethodPatch, "/tasks/"+created.ID, patchBody)
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRec.Code, patchRec.Body.String())
	}
	var patched Task
	if err := json.NewDecoder(patchRec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if !patched.Done {
		t.Fatalf("patched.Done = false, want true")
	}
}
