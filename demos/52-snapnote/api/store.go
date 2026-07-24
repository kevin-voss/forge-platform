package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// NoteStore persists notes and attachment metadata in Postgres.
type NoteStore interface {
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close() error
	ListNotes(ctx context.Context) ([]*Note, error)
	GetNote(ctx context.Context, id string) (*Note, error)
	CreateNote(ctx context.Context, title, body string) (*Note, error)
	PatchNote(ctx context.Context, id string, title, body *string) (*Note, error)
	DeleteNote(ctx context.Context, id string) error
	ListAttachments(ctx context.Context, noteID string) ([]*Attachment, error)
	GetAttachment(ctx context.Context, noteID, attachmentID string) (*Attachment, error)
	CreateAttachment(ctx context.Context, noteID, filename, contentType string) (*Attachment, error)
}

type pgStore struct {
	db            *sql.DB
	migrationsDir string
}

func openStore(databaseURL, migrationsDir string) (*pgStore, error) {
	url := strings.TrimSpace(databaseURL)
	if url == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if strings.Contains(url, "postgres:5432/forge") || strings.Contains(url, ":5001/forge") {
		return nil, errors.New("refusing Control database URL")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &pgStore{db: db, migrationsDir: migrationsDir}, nil
}

func (s *pgStore) Close() error { return s.db.Close() }

func (s *pgStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *pgStore) Migrate(ctx context.Context) error {
	return applyMigrations(ctx, s.db, s.migrationsDir)
}

func (s *pgStore) ListNotes(ctx context.Context) ([]*Note, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, body, created_at, updated_at
		FROM notes
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Note, 0)
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (s *pgStore) GetNote(ctx context.Context, id string) (*Note, error) {
	var n Note
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, body, created_at, updated_at
		FROM notes WHERE id = $1
	`, id).Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *pgStore) CreateNote(ctx context.Context, title, body string) (*Note, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errEmptyTitle
	}
	now := time.Now().UTC()
	note := &Note{
		ID:        newID(),
		Title:     title,
		Body:      body,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notes (id, title, body, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`, note.ID, note.Title, note.Body, note.CreatedAt, note.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return note, nil
}

func (s *pgStore) PatchNote(ctx context.Context, id string, title, body *string) (*Note, error) {
	note, err := s.GetNote(ctx, id)
	if err != nil {
		return nil, err
	}
	if note == nil {
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
	_, err = s.db.ExecContext(ctx, `
		UPDATE notes SET title = $2, body = $3, updated_at = $4 WHERE id = $1
	`, note.ID, note.Title, note.Body, note.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return note, nil
}

func (s *pgStore) DeleteNote(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
}

func (s *pgStore) ListAttachments(ctx context.Context, noteID string) ([]*Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, note_id, object_key, content_type, status, thumbnail_key, created_at, updated_at
		FROM attachments
		WHERE note_id = $1
		ORDER BY created_at ASC, id ASC
	`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Attachment, 0)
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *pgStore) GetAttachment(ctx context.Context, noteID, attachmentID string) (*Attachment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, note_id, object_key, content_type, status, thumbnail_key, created_at, updated_at
		FROM attachments
		WHERE note_id = $1 AND id = $2
	`, noteID, attachmentID)
	a, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *pgStore) CreateAttachment(ctx context.Context, noteID, filename, contentType string) (*Attachment, error) {
	note, err := s.GetNote(ctx, noteID)
	if err != nil {
		return nil, err
	}
	if note == nil {
		return nil, errNotFound
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	now := time.Now().UTC()
	id := newID()
	att := &Attachment{
		ID:          id,
		NoteID:      noteID,
		ObjectKey:   attachmentObjectKey(noteID, id, filename),
		ContentType: ct,
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO attachments (id, note_id, object_key, content_type, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, att.ID, att.NoteID, att.ObjectKey, att.ContentType, att.Status, att.CreatedAt, att.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return att, nil
}

type attachmentScanner interface {
	Scan(dest ...any) error
}

func scanAttachment(row attachmentScanner) (*Attachment, error) {
	var a Attachment
	var thumb sql.NullString
	if err := row.Scan(
		&a.ID, &a.NoteID, &a.ObjectKey, &a.ContentType, &a.Status,
		&thumb, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if thumb.Valid {
		a.ThumbnailKey = thumb.String
	}
	return &a, nil
}

var (
	errNotFound   = errors.New("not found")
	errEmptyTitle = errors.New("title is required")
)
