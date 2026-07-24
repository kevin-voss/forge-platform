package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Note is a persisted note row.
type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Attachment is metadata for a file attached to a note.
type Attachment struct {
	ID           string    `json:"id"`
	NoteID       string    `json:"noteId"`
	ObjectKey    string    `json:"objectKey"`
	ContentType  string    `json:"contentType"`
	Status       string    `json:"status"`
	ThumbnailKey string    `json:"thumbnailKey,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type createNoteRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type patchNoteRequest struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
}

type createAttachmentRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
}

type createAttachmentResponse struct {
	Attachment  *Attachment `json:"attachment"`
	UploadURL   string      `json:"uploadUrl"`
	UploadMethod string     `json:"uploadMethod"`
	ExpiresAt   string      `json:"expiresAt,omitempty"`
}

type downloadAttachmentResponse struct {
	DownloadURL    string `json:"downloadUrl"`
	DownloadMethod string `json:"downloadMethod"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
	ObjectKey      string `json:"objectKey"`
}

type server struct {
	store   NoteStore
	storage *storageClient
	events  *eventsClient
}

func newServer(store NoteStore, storage *storageClient, events *eventsClient) *server {
	return &server{store: store, storage: storage, events: events}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /notes", s.handleListNotes)
	mux.HandleFunc("POST /notes", s.handleCreateNote)
	mux.HandleFunc("GET /notes/{id}", s.handleGetNote)
	mux.HandleFunc("PATCH /notes/{id}", s.handlePatchNote)
	mux.HandleFunc("DELETE /notes/{id}", s.handleDeleteNote)
	mux.HandleFunc("GET /notes/{id}/attachments", s.handleListAttachments)
	mux.HandleFunc("POST /notes/{id}/attachments", s.handleCreateAttachment)
	mux.HandleFunc("GET /notes/{id}/attachments/{attachmentId}", s.handleGetAttachment)
	mux.HandleFunc("POST /notes/{id}/attachments/{attachmentId}/complete", s.handleCompleteAttachment)
	mux.HandleFunc("GET /notes/{id}/attachments/{attachmentId}/download", s.handleDownloadAttachment)
	mux.HandleFunc("GET /notes/{id}/attachments/{attachmentId}/content", s.handleStreamAttachment)
	return withCORS(mux)
}

func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"error":  "database unavailable",
		})
		return
	}
	if s.storage != nil && s.storage.enabled() {
		if err := s.storage.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"error":  "storage unavailable",
			})
			return
		}
	}
	if s.events != nil && s.events.enabled() {
		if err := s.events.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not_ready",
				"error":  "events unavailable",
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	notes, err := s.store.ListNotes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var req createNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	note, err := s.store.CreateNote(r.Context(), req.Title, req.Body)
	if errors.Is(err, errEmptyTitle) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, note)
}

func (s *server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	note, err := s.store.GetNote(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
		return
	}
	if note == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (s *server) handlePatchNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req patchNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	note, err := s.store.PatchNote(r.Context(), id, req.Title, req.Body)
	if errors.Is(err, errEmptyTitle) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "patch failed"})
		return
	}
	if note == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (s *server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.DeleteNote(r.Context(), id)
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListAttachments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	note, err := s.store.GetNote(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
		return
	}
	if note == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	items, err := s.store.ListAttachments(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) handleCreateAttachment(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil || !s.storage.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return
	}
	noteID := r.PathValue("id")
	var req createAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Filename) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename is required"})
		return
	}
	att, err := s.store.CreateAttachment(r.Context(), noteID, req.Filename, req.ContentType)
	if errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	uploadURL, expiresAt, err := s.storage.Sign(r.Context(), http.MethodPut, att.ObjectKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "presign failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, createAttachmentResponse{
		Attachment:   att,
		UploadURL:    uploadURL,
		UploadMethod: http.MethodPut,
		ExpiresAt:    expiresAt,
	})
}

func (s *server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	noteID := r.PathValue("id")
	attID := r.PathValue("attachmentId")
	att, err := s.store.GetAttachment(r.Context(), noteID, attID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
		return
	}
	if att == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, att)
}

// handleCompleteAttachment is called after the browser PUTs the object; publishes
// attachment.uploaded to the durable queue (Idempotency-Key = attachment_id).
func (s *server) handleCompleteAttachment(w http.ResponseWriter, r *http.Request) {
	if s.events == nil || !s.events.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "events unavailable"})
		return
	}
	noteID := r.PathValue("id")
	attID := r.PathValue("attachmentId")
	att, err := s.store.GetAttachment(r.Context(), noteID, attID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
		return
	}
	if att == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Already processed — return current row; publish is idempotent by attachment_id.
	if att.Status == "ready" {
		writeJSON(w, http.StatusOK, att)
		return
	}
	if err := s.events.PublishAttachmentUploaded(r.Context(), att); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":  "events publish failed",
			"detail": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusAccepted, att)
}

func (s *server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil || !s.storage.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return
	}
	noteID := r.PathValue("id")
	attID := r.PathValue("attachmentId")
	att, err := s.store.GetAttachment(r.Context(), noteID, attID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
		return
	}
	if att == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	downloadURL, expiresAt, err := s.storage.Sign(r.Context(), http.MethodGet, att.ObjectKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "presign failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, downloadAttachmentResponse{
		DownloadURL:    downloadURL,
		DownloadMethod: http.MethodGet,
		ExpiresAt:      expiresAt,
		ObjectKey:      att.ObjectKey,
	})
}

func (s *server) handleStreamAttachment(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil || !s.storage.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return
	}
	noteID := r.PathValue("id")
	attID := r.PathValue("attachmentId")
	att, err := s.store.GetAttachment(r.Context(), noteID, attID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get failed"})
		return
	}
	if att == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	body, ct, err := s.storage.GetObject(r.Context(), att.ObjectKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "storage get failed", "detail": err.Error()})
		return
	}
	defer body.Close()
	if att.ContentType != "" {
		ct = att.ContentType
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
