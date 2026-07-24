package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memStore struct {
	mu   sync.Mutex
	atts map[string]*Attachment
}

func newMemStore(att *Attachment) *memStore {
	m := &memStore{atts: make(map[string]*Attachment)}
	if att != nil {
		cp := *att
		m.atts[att.ID] = &cp
	}
	return m
}

func (m *memStore) Ping(context.Context) error { return nil }
func (m *memStore) Close() error                { return nil }

func (m *memStore) GetByID(_ context.Context, id string) (*Attachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.atts[id]
	if !ok {
		return nil, nil
	}
	cp := *a
	return &cp, nil
}

func (m *memStore) MarkReady(_ context.Context, id, thumbnailKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.atts[id]
	if !ok {
		return nil
	}
	a.Status = "ready"
	a.ThumbnailKey = thumbnailKey
	return nil
}

type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    map[string]int
}

func newMemObjects() *memObjects {
	return &memObjects{
		objects: make(map[string][]byte),
		puts:    make(map[string]int),
	}
}

func (m *memObjects) GetObject(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, errNotFound{key}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (m *memObjects) PutObject(_ context.Context, key string, _ string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objects[key] = cp
	m.puts[key]++
	return nil
}

type errNotFound struct{ key string }

func (e errNotFound) Error() string { return "not found: " + e.key }

func TestProcessAttachmentIdempotent(t *testing.T) {
	att := &Attachment{
		ID:          "att-1",
		NoteID:      "note-1",
		ObjectKey:   "notes/note-1/att-1/lake.jpg",
		ContentType: "image/jpeg",
		Status:      "pending",
	}
	store := newMemStore(att)
	objects := newMemObjects()
	objects.objects[att.ObjectKey] = []byte("fake-jpeg-bytes")
	h := newJobHandler(store, objects)

	payload := uploadedPayload{
		AttachmentID: att.ID,
		NoteID:       att.NoteID,
		ObjectKey:    att.ObjectKey,
		ContentType:  att.ContentType,
	}
	key1, err := h.ProcessAttachment(t.Context(), payload)
	if err != nil {
		t.Fatalf("first process: %v", err)
	}
	key2, err := h.ProcessAttachment(t.Context(), payload)
	if err != nil {
		t.Fatalf("second process: %v", err)
	}
	if key1 == "" || key1 != key2 {
		t.Fatalf("thumbnail keys differ: %q vs %q", key1, key2)
	}
	if objects.puts[key1] != 1 {
		t.Fatalf("thumbnail PUT count = %d, want 1 (idempotent)", objects.puts[key1])
	}
	got, err := store.GetByID(t.Context(), att.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "ready" || got.ThumbnailKey != key1 {
		t.Fatalf("attachment after process: %+v", got)
	}
}

func TestPublishConsumeRoundTrip(t *testing.T) {
	// Fake Events: accept publish, deliver once on consume, track processed+ack.
	var (
		mu        sync.Mutex
		pending   []deliveredMessage
		processed = map[string]bool{}
		acked     = map[string]bool{}
		published int
	)
	eventsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health/ready":
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/consumers":
			writeJSON(w, http.StatusCreated, map[string]string{"name": "snapnote-attachments"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/events":
			var body struct {
				Data uploadedPayload `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			idem := r.Header.Get("Idempotency-Key")
			mu.Lock()
			published++
			eventID := idem
			if eventID == "" {
				eventID = "evt-" + body.Data.AttachmentID
			}
			raw, _ := json.Marshal(body.Data)
			pending = append(pending, deliveredMessage{
				EventID:  eventID,
				Subject:  "attachment.uploaded",
				AckToken: "ack-" + eventID,
				Data:     raw,
			})
			mu.Unlock()
			writeJSON(w, http.StatusAccepted, map[string]any{"event_id": eventID, "seq": 1})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/consume":
			mu.Lock()
			var out []deliveredMessage
			for _, m := range pending {
				if processed[m.EventID] {
					acked[m.AckToken] = true
					continue
				}
				out = append(out, m)
			}
			pending = nil
			mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"messages": out})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/processed":
			var body struct {
				EventID string `json:"event_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			processed[body.EventID] = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/ack":
			var body struct {
				AckToken string `json:"ack_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			acked[body.AckToken] = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/nak":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer eventsSrv.Close()

	att := &Attachment{
		ID:          "att-rt",
		NoteID:      "note-rt",
		ObjectKey:   "notes/note-rt/att-rt/pic.png",
		ContentType: "image/png",
		Status:      "pending",
	}
	store := newMemStore(att)
	objects := newMemObjects()
	objects.objects[att.ObjectKey] = []byte("png-bytes")

	// Publish (same shape as API client).
	pubBody, _ := json.Marshal(map[string]any{
		"subject": "attachment.uploaded",
		"source":  "snapnote-api",
		"data": uploadedPayload{
			AttachmentID: att.ID,
			NoteID:       att.NoteID,
			ObjectKey:    att.ObjectKey,
			ContentType:  att.ContentType,
		},
	})
	req, _ := http.NewRequest(http.MethodPost, eventsSrv.URL+"/v1/events", bytes.NewReader(pubBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", att.ID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("publish status=%d", resp.StatusCode)
	}

	events := newEventsClient(eventsConfig{
		BaseURL:       eventsSrv.URL,
		Consumer:      "snapnote-attachments",
		Identity:      "snapnote-attachments",
		Subject:       "attachment.uploaded",
		AckWaitS:      5,
		MaxDeliveries: 3,
		Batch:         8,
	})
	h := newJobHandler(store, objects)

	msgs, err := events.Consume(t.Context())
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if err := handleMessage(t.Context(), events, h, msgs[0]); err != nil {
		t.Fatalf("handle: %v", err)
	}

	got, _ := store.GetByID(t.Context(), att.ID)
	if got.Status != "ready" || !strings.HasSuffix(got.ThumbnailKey, "thumb.bin") {
		t.Fatalf("after round-trip: %+v", got)
	}
	mu.Lock()
	if published != 1 || !processed[att.ID] || !acked["ack-"+att.ID] {
		t.Fatalf("published=%d processed=%v acked=%v", published, processed, acked)
	}
	mu.Unlock()

	// Redelivery of same logical message: already ready → no second thumbnail PUT.
	msgs2 := []deliveredMessage{{
		EventID:  att.ID,
		AckToken: "ack-redeliver",
		Data:     msgs[0].Data,
	}}
	if err := handleMessage(t.Context(), events, h, msgs2[0]); err != nil {
		t.Fatalf("redeliver handle: %v", err)
	}
	thumbKey := got.ThumbnailKey
	if objects.puts[thumbKey] != 1 {
		t.Fatalf("after redelivery PUT count = %d, want 1", objects.puts[thumbKey])
	}
}
