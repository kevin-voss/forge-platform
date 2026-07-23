package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type incidentStore interface {
	Migrate(ctx context.Context) error
	Create(ctx context.Context, inc incident) error
	Get(ctx context.Context, id string) (incident, bool, error)
	List(ctx context.Context) ([]incident, error)
	Ready(ctx context.Context) error
	Close() error
	Backend() string
}

type memoryStore struct {
	incidents map[string]incident
}

func newMemoryStore() *memoryStore {
	return &memoryStore{incidents: make(map[string]incident)}
}

func (m *memoryStore) Migrate(context.Context) error { return nil }

func (m *memoryStore) Create(_ context.Context, inc incident) error {
	m.incidents[inc.ID] = inc
	return nil
}

func (m *memoryStore) Get(_ context.Context, id string) (incident, bool, error) {
	inc, ok := m.incidents[id]
	return inc, ok, nil
}

func (m *memoryStore) List(context.Context) ([]incident, error) {
	out := make([]incident, 0, len(m.incidents))
	for _, inc := range m.incidents {
		out = append(out, inc)
	}
	return out, nil
}

func (m *memoryStore) Ready(context.Context) error { return nil }
func (m *memoryStore) Close() error                 { return nil }
func (m *memoryStore) Backend() string              { return "memory" }

type pgStore struct {
	db *sql.DB
}

func openStore(databaseURL string) (incidentStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return newMemoryStore(), nil
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &pgStore{db: db}, nil
}

func (p *pgStore) Migrate(ctx context.Context) error {
	sqlBytes, err := migrationFS.ReadFile("migrations/001_incidents.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if _, err := p.db.ExecContext(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}
	return nil
}

func (p *pgStore) Create(ctx context.Context, inc incident) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO incidents (id, title, description, severity, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, inc.ID, inc.Title, inc.Description, inc.Severity, inc.Status, inc.CreatedAt.UTC())
	return err
}

func (p *pgStore) Get(ctx context.Context, id string) (incident, bool, error) {
	var inc incident
	err := p.db.QueryRowContext(ctx, `
		SELECT id, title, description, severity, status, created_at
		FROM incidents WHERE id = $1
	`, id).Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &inc.CreatedAt)
	if err == sql.ErrNoRows {
		return incident{}, false, nil
	}
	if err != nil {
		return incident{}, false, err
	}
	return inc, true, nil
}

func (p *pgStore) List(ctx context.Context) ([]incident, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, title, description, severity, status, created_at
		FROM incidents ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]incident, 0)
	for rows.Next() {
		var inc incident
		if err := rows.Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &inc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

func (p *pgStore) Ready(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

func (p *pgStore) Close() error { return p.db.Close() }
func (p *pgStore) Backend() string {
	return "postgres"
}
