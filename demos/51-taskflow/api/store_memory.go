package main

import (
	"context"
	"strings"
	"sync"
	"time"
)

// memoryStore is a test-only TaskStore (production uses Postgres).
type memoryStore struct {
	mu       sync.Mutex
	tasks    map[string]*Task
	order    []string
	users    map[string]*User // by id
	byEmail  map[string]string
	settings map[string]string
	projects map[string]bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		tasks:    make(map[string]*Task),
		users:    make(map[string]*User),
		byEmail:  make(map[string]string),
		settings: make(map[string]string),
		projects: map[string]bool{"project-default": true, "project-shared": true},
	}
}

func (m *memoryStore) Migrate(context.Context) error             { return nil }
func (m *memoryStore) Ping(context.Context) error                { return nil }
func (m *memoryStore) Close() error                              { return nil }
func (m *memoryStore) EnsureDefaultProject(context.Context) (string, error) {
	return "project-default", nil
}

func (m *memoryStore) ListTasks(context.Context) ([]*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Task, 0, len(m.order))
	for _, id := range m.order {
		if t, ok := m.tasks[id]; ok {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *memoryStore) GetTask(_ context.Context, id string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (m *memoryStore) CreateTask(_ context.Context, title string) (*Task, error) {
	now := time.Now().UTC()
	task := &Task{ID: newID(), Title: title, Done: false, CreatedAt: now, UpdatedAt: now}
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.order = append(m.order, task.ID)
	m.mu.Unlock()
	cp := *task
	return &cp, nil
}

func (m *memoryStore) PatchTask(_ context.Context, id string, title *string, done *bool) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return nil, errEmptyTitle
		}
		task.Title = trimmed
	}
	if done != nil {
		task.Done = *done
	}
	task.UpdatedAt = time.Now().UTC()
	cp := *task
	return &cp, nil
}

func (m *memoryStore) DeleteTask(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		return errNotFound
	}
	delete(m.tasks, id)
	next := m.order[:0]
	for _, existing := range m.order {
		if existing != id {
			next = append(next, existing)
		}
	}
	m.order = next
	return nil
}

func (m *memoryStore) UpsertUser(_ context.Context, id, email, _ /*passwordHash*/, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	if role != "admin" && role != "member" {
		role = "member"
	}
	u := &User{ID: id, Email: email, Role: role}
	m.users[id] = u
	m.byEmail[email] = id
	return nil
}

func (m *memoryStore) GetUserByID(_ context.Context, id string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return nil, nil
	}
	cp := *u
	return &cp, nil
}

func (m *memoryStore) GetUserByEmail(_ context.Context, email string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byEmail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return nil, nil
	}
	u := m.users[id]
	cp := *u
	return &cp, nil
}

func (m *memoryStore) GetSetting(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[key], nil
}

func (m *memoryStore) SetSetting(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}

func (m *memoryStore) DeleteProject(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.projects[id] {
		return errNotFound
	}
	delete(m.projects, id)
	return nil
}
