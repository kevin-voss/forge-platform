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

// Attachment is the subset of metadata the worker needs.
type Attachment struct {
	ID           string
	NoteID       string
	ObjectKey    string
	ContentType  string
	Status       string
	ThumbnailKey string
}

type attachmentStore interface {
	Ping(ctx context.Context) error
	Close() error
	GetByID(ctx context.Context, id string) (*Attachment, error)
	MarkReady(ctx context.Context, id, thumbnailKey string) error
}

type pgStore struct {
	db *sql.DB
}

func openStore(databaseURL string) (*pgStore, error) {
	url := strings.TrimSpace(databaseURL)
	if url == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &pgStore{db: db}, nil
}

func (s *pgStore) Close() error { return s.db.Close() }

func (s *pgStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *pgStore) GetByID(ctx context.Context, id string) (*Attachment, error) {
	var a Attachment
	var thumb sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, note_id, object_key, content_type, status, thumbnail_key
		FROM attachments WHERE id = $1
	`, id).Scan(&a.ID, &a.NoteID, &a.ObjectKey, &a.ContentType, &a.Status, &thumb)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if thumb.Valid {
		a.ThumbnailKey = thumb.String
	}
	return &a, nil
}

// MarkReady flips status to ready and sets thumbnail_key. Idempotent when already ready
// with the same thumbnail key.
func (s *pgStore) MarkReady(ctx context.Context, id, thumbnailKey string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE attachments
		SET status = 'ready',
		    thumbnail_key = $2,
		    updated_at = $3
		WHERE id = $1
		  AND (status <> 'ready' OR thumbnail_key IS DISTINCT FROM $2)
	`, id, thumbnailKey, time.Now().UTC())
	if err != nil {
		return err
	}
	_, _ = res.RowsAffected()
	return nil
}
