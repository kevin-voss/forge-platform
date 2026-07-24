package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestHealthReady(t *testing.T) {
	srv := newServer(newMemoryStore(), nil)
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

func TestNotesCRUDStub(t *testing.T) {
	srv := newServer(newMemoryStore(), nil)
	handler := srv.routes()

	createBody := bytes.NewBufferString(`{"title":"Trip photos","body":"Lake day"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/notes", createBody)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRec.Code, createRec.Body.String())
	}
	var created Note
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Title != "Trip photos" || created.Body != "Lake day" {
		t.Fatalf("unexpected created note: %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/notes", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRec.Code)
	}
	var listed []*Note
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list = %+v, want one note %s", listed, created.ID)
	}

	patchBody := bytes.NewBufferString(`{"body":"Updated body"}`)
	patchReq := httptest.NewRequest(http.MethodPatch, "/notes/"+created.ID, patchBody)
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRec.Code, patchRec.Body.String())
	}
	var patched Note
	if err := json.NewDecoder(patchRec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Body != "Updated body" {
		t.Fatalf("patched.Body = %q, want Updated body", patched.Body)
	}

	attReq := httptest.NewRequest(http.MethodGet, "/notes/"+created.ID+"/attachments", nil)
	attRec := httptest.NewRecorder()
	handler.ServeHTTP(attRec, attReq)
	if attRec.Code != http.StatusOK {
		t.Fatalf("attachments status = %d, want 200", attRec.Code)
	}
}

func TestAttachmentPresignPutGetRoundTrip(t *testing.T) {
	fake := newFakeStorage(t)
	defer fake.Close()

	cfg := storageConfig{
		BaseURL:    fake.URL,
		PublicURL:  fake.URL,
		ProjectID:  "snapnote",
		Bucket:     "snapnote-attachments",
		SignTTLSec: 300,
	}
	storage := newStorageClient(cfg)
	if err := storage.EnsureBucket(t.Context()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	store := newMemoryStore()
	srv := newServer(store, storage)
	handler := srv.routes()

	createNote := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewBufferString(`{"title":"Trip","body":""}`))
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createNote)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create note: %d %s", createRec.Code, createRec.Body.String())
	}
	var note Note
	if err := json.NewDecoder(createRec.Body).Decode(&note); err != nil {
		t.Fatalf("decode note: %v", err)
	}

	attBody := bytes.NewBufferString(`{"filename":"lake.jpg","contentType":"image/jpeg"}`)
	attReq := httptest.NewRequest(http.MethodPost, "/notes/"+note.ID+"/attachments", attBody)
	attRec := httptest.NewRecorder()
	handler.ServeHTTP(attRec, attReq)
	if attRec.Code != http.StatusCreated {
		t.Fatalf("create attachment: %d %s", attRec.Code, attRec.Body.String())
	}
	var created createAttachmentResponse
	if err := json.NewDecoder(attRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	if created.Attachment == nil || created.Attachment.Status != "pending" {
		t.Fatalf("attachment status: %+v", created.Attachment)
	}
	wantPrefix := "notes/" + note.ID + "/" + created.Attachment.ID + "/"
	if !strings.HasPrefix(created.Attachment.ObjectKey, wantPrefix) {
		t.Fatalf("objectKey = %q, want prefix %q", created.Attachment.ObjectKey, wantPrefix)
	}
	if created.UploadURL == "" || created.UploadMethod != http.MethodPut {
		t.Fatalf("uploadUrl/method missing: %+v", created)
	}

	payload := []byte("fake-jpeg-bytes")
	putReq, err := http.NewRequest(http.MethodPut, created.UploadURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("put request: %v", err)
	}
	putReq.Header.Set("Content-Type", "image/jpeg")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated && putResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(putResp.Body)
		t.Fatalf("put status=%d body=%s", putResp.StatusCode, string(b))
	}

	dlReq := httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/attachments/"+created.Attachment.ID+"/download", nil)
	dlRec := httptest.NewRecorder()
	handler.ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK {
		t.Fatalf("download: %d %s", dlRec.Code, dlRec.Body.String())
	}
	var dl downloadAttachmentResponse
	if err := json.NewDecoder(dlRec.Body).Decode(&dl); err != nil {
		t.Fatalf("decode download: %v", err)
	}
	if dl.ObjectKey != created.Attachment.ObjectKey {
		t.Fatalf("download objectKey = %q, want %q", dl.ObjectKey, created.Attachment.ObjectKey)
	}

	getResp, err := http.Get(dl.DownloadURL)
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getResp.Body)
		t.Fatalf("get status=%d body=%s", getResp.StatusCode, string(b))
	}
	got, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read get body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded bytes mismatch: got %q want %q", got, payload)
	}

	streamReq := httptest.NewRequest(http.MethodGet, "/notes/"+note.ID+"/attachments/"+created.Attachment.ID+"/content", nil)
	streamRec := httptest.NewRecorder()
	handler.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != http.StatusOK {
		t.Fatalf("stream: %d %s", streamRec.Code, streamRec.Body.String())
	}
	if !bytes.Equal(streamRec.Body.Bytes(), payload) {
		t.Fatalf("streamed bytes mismatch")
	}
}

type fakeStorage struct {
	*httptest.Server
	mu      sync.Mutex
	buckets map[string]bool
	objects map[string][]byte
	tokens  map[string]string // token → "METHOD|key"
}

func newFakeStorage(t *testing.T) *fakeStorage {
	t.Helper()
	fs := &fakeStorage{
		buckets: make(map[string]bool),
		objects: make(map[string][]byte),
		tokens:  make(map[string]string),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/buckets", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if fs.buckets[body.Name] {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "exists"})
			return
		}
		fs.buckets[body.Name] = true
		writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name})
	})
	mux.HandleFunc("POST /v1/buckets/{bucket}/objects/{key...}", func(w http.ResponseWriter, r *http.Request) {
		keyPath := r.PathValue("key")
		if !strings.HasSuffix(keyPath, "/sign") {
			http.NotFound(w, r)
			return
		}
		key := strings.TrimSuffix(keyPath, "/sign")
		var body struct {
			Method     string `json:"method"`
			TTLSeconds int64  `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		token := "tok-" + newID()
		fs.mu.Lock()
		fs.tokens[token] = body.Method + "|" + key
		fs.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"token":      token,
			"url":        "/v1/buckets/" + r.PathValue("bucket") + "/objects/" + key + "?token=" + token,
			"expires_at": "2099-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("PUT /v1/buckets/{bucket}/objects/{key...}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		token := r.URL.Query().Get("token")
		fs.mu.Lock()
		defer fs.mu.Unlock()
		scope, ok := fs.tokens[token]
		if !ok || scope != "PUT|"+key {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_token"})
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read failed"})
			return
		}
		fs.objects[key] = data
		writeJSON(w, http.StatusCreated, map[string]any{"key": key, "size": len(data)})
	})
	mux.HandleFunc("GET /v1/buckets/{bucket}/objects/{key...}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		token := r.URL.Query().Get("token")
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if token != "" {
			scope, ok := fs.tokens[token]
			if !ok || scope != "GET|"+key {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_token"})
				return
			}
		} else if r.Header.Get("X-Forge-Project") == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing project"})
			return
		}
		data, ok := fs.objects[key]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	fs.Server = httptest.NewServer(mux)
	return fs
}
