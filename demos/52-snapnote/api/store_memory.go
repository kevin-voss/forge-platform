package main

import (
	"context"
	"strings"
	"sync"
	"time"
)

// memoryStore is a test-only NoteStore (production uses Postgres).
type memoryStore struct {
	mu          sync.Mutex
	notes       map[string]*Note
	order       []string
	attachments map[string][]*Attachment
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		notes:       make(map[string]*Note),
		attachments: make(map[string][]*Attachment),
	}
}

func (m *memoryStore) Migrate(context.Context) error { return nil }
func (m *memoryStore) Ping(context.Context) error    { return nil }
func (m *memoryStore) Close() error                  { return nil }

func (m *memoryStore) ListNotes(context.Context) ([]*Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Note, 0, len(m.order))
	for _, id := range m.order {
		if n, ok := m.notes[id]; ok {
			cp := *n
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *memoryStore) GetNote(_ context.Context, id string) (*Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.notes[id]
	if !ok {
		return nil, nil
	}
	cp := *n
	return &cp, nil
}

func (m *memoryStore) CreateNote(_ context.Context, title, body string) (*Note, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errEmptyTitle
	}
	now := time.Now().UTC()
	note := &Note{ID: newID(), Title: title, Body: body, CreatedAt: now, UpdatedAt: now}
	m.mu.Lock()
	m.notes[note.ID] = note
	m.order = append(m.order, note.ID)
	m.mu.Unlock()
	cp := *note
	return &cp, nil
}

func (m *memoryStore) PatchNote(_ context.Context, id string, title, body *string) (*Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	note, ok := m.notes[id]
	if !ok {
		return nil, nil
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return nil, errEmptyTitle
		}
		note.Title = trimmed
	}
	if body != nil {
		note.Body = *body
	}
	note.UpdatedAt = time.Now().UTC()
	cp := *note
	return &cp, nil
}

func (m *memoryStore) DeleteNote(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.notes[id]; !ok {
		return errNotFound
	}
	delete(m.notes, id)
	delete(m.attachments, id)
	next := m.order[:0]
	for _, existing := range m.order {
		if existing != id {
			next = append(next, existing)
		}
	}
	m.order = next
	return nil
}

func (m *memoryStore) ListAttachments(_ context.Context, noteID string) ([]*Attachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.attachments[noteID]
	out := make([]*Attachment, 0, len(items))
	for _, a := range items {
		cp := *a
		out = append(out, &cp)
	}
	return out, nil
}
