package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

type uploadedPayload struct {
	AttachmentID string `json:"attachment_id"`
	NoteID       string `json:"note_id"`
	ObjectKey    string `json:"object_key"`
	ContentType  string `json:"content_type"`
}

// objectStore is the storage surface used by the worker handler (testable).
type objectStore interface {
	GetObject(ctx context.Context, key string) ([]byte, error)
	PutObject(ctx context.Context, key string, contentType string, data []byte) error
}

type jobHandler struct {
	store   attachmentStore
	storage objectStore
	mu      sync.Mutex
	// putCounts tracks thumbnail PUTs by key for idempotency tests.
	putCounts map[string]int
}

func newJobHandler(store attachmentStore, storage objectStore) *jobHandler {
	return &jobHandler{
		store:     store,
		storage:   storage,
		putCounts: make(map[string]int),
	}
}

// ProcessAttachment generates a thumbnail exactly once for the attachment_id.
// Safe under redelivery: already-ready rows skip PUT/side effects.
func (h *jobHandler) ProcessAttachment(ctx context.Context, payload uploadedPayload) (thumbnailKey string, err error) {
	if payload.AttachmentID == "" {
		return "", fmt.Errorf("attachment_id required")
	}
	att, err := h.store.GetByID(ctx, payload.AttachmentID)
	if err != nil {
		return "", err
	}
	if att == nil {
		return "", fmt.Errorf("attachment %s not found", payload.AttachmentID)
	}
	if att.Status == "ready" && att.ThumbnailKey != "" {
		return att.ThumbnailKey, nil
	}

	objectKey := payload.ObjectKey
	if objectKey == "" {
		objectKey = att.ObjectKey
	}
	content, err := h.storage.GetObject(ctx, objectKey)
	if err != nil {
		return "", fmt.Errorf("get object: %w", err)
	}
	thumbKey, thumb := makeThumbnail(objectKey, content)

	// Idempotency guard: if a concurrent redelivery already marked ready, skip PUT.
	again, err := h.store.GetByID(ctx, payload.AttachmentID)
	if err != nil {
		return "", err
	}
	if again != nil && again.Status == "ready" && again.ThumbnailKey != "" {
		return again.ThumbnailKey, nil
	}

	if err := h.storage.PutObject(ctx, thumbKey, "application/octet-stream", thumb); err != nil {
		return "", fmt.Errorf("put thumbnail: %w", err)
	}
	h.mu.Lock()
	h.putCounts[thumbKey]++
	h.mu.Unlock()

	if err := h.store.MarkReady(ctx, payload.AttachmentID, thumbKey); err != nil {
		return "", fmt.Errorf("mark ready: %w", err)
	}
	return thumbKey, nil
}

func (h *jobHandler) ThumbnailPutCount(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.putCounts[key]
}

func parseUploaded(data json.RawMessage) (uploadedPayload, error) {
	var p uploadedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if p.AttachmentID == "" {
		return p, fmt.Errorf("missing attachment_id")
	}
	return p, nil
}

// handleMessage processes one delivered event: side effects → processed → ack.
// On failure, nak for redelivery (at-least-once + app idempotency ⇒ exactly-once effects).
func handleMessage(ctx context.Context, events *eventsClient, h *jobHandler, msg deliveredMessage) error {
	payload, err := parseUploaded(msg.Data)
	if err != nil {
		log.Printf("poison message event_id=%s: %v", msg.EventID, err)
		_ = events.Nak(ctx, msg.AckToken, 0)
		return err
	}
	thumbKey, err := h.ProcessAttachment(ctx, payload)
	if err != nil {
		log.Printf("process attachment_id=%s event_id=%s: %v", payload.AttachmentID, msg.EventID, err)
		_ = events.Nak(ctx, msg.AckToken, 2)
		return err
	}
	if err := events.MarkProcessed(ctx, msg.EventID); err != nil {
		log.Printf("mark processed event_id=%s: %v", msg.EventID, err)
		_ = events.Nak(ctx, msg.AckToken, 2)
		return err
	}
	if err := events.Ack(ctx, msg.AckToken); err != nil {
		log.Printf("ack event_id=%s: %v", msg.EventID, err)
		return err
	}
	log.Printf("processed attachment_id=%s event_id=%s thumbnail=%s delivery=%d",
		payload.AttachmentID, msg.EventID, thumbKey, msg.DeliveryCount)
	return nil
}
