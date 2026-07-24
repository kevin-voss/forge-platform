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

// TaskStore persists tasks (and related seed tables) in Postgres.
type TaskStore interface {
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error
	Close() error
	ListTasks(ctx context.Context) ([]*Task, error)
	GetTask(ctx context.Context, id string) (*Task, error)
	CreateTask(ctx context.Context, title string) (*Task, error)
	PatchTask(ctx context.Context, id string, title *string, done *bool) (*Task, error)
	DeleteTask(ctx context.Context, id string) error
	EnsureDefaultProject(ctx context.Context) (string, error)
	UpsertUser(ctx context.Context, id, email, passwordHash, role string) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteProject(ctx context.Context, id string) error
}

type pgStore struct {
	db             *sql.DB
	migrationsDir  string
	defaultProject string
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
	if err := applyMigrations(ctx, s.db, s.migrationsDir); err != nil {
		return err
	}
	_, err := s.EnsureDefaultProject(ctx)
	return err
}

// EnsureDefaultProject creates a bootstrap owner + shared project when seed has
// not run yet, so /tasks can persist before seed.sh.
func (s *pgStore) EnsureDefaultProject(ctx context.Context) (string, error) {
	if s.defaultProject != "" {
		return s.defaultProject, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM projects ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	if err == nil {
		s.defaultProject = id
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	ownerID := "user-bootstrap"
	projectID := "project-default"
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, role)
		VALUES ($1, $2, $3, 'admin')
		ON CONFLICT (email) DO NOTHING
	`, ownerID, "bootstrap@taskflow.local", "bootstrap-not-for-login")
	if err != nil {
		return "", fmt.Errorf("ensure bootstrap user: %w", err)
	}
	// Resolve owner id if the email already existed under a different id.
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, "bootstrap@taskflow.local").Scan(&ownerID); err != nil {
		return "", fmt.Errorf("lookup bootstrap user: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO projects (id, name, owner_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, projectID, "Shared", ownerID)
	if err != nil {
		return "", fmt.Errorf("ensure default project: %w", err)
	}
	s.defaultProject = projectID
	return projectID, nil
}

func (s *pgStore) ListTasks(ctx context.Context) ([]*Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, done, created_at, updated_at
		FROM tasks
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Task, 0)
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *pgStore) GetTask(ctx context.Context, id string) (*Task, error) {
	var t Task
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, done, created_at, updated_at
		FROM tasks WHERE id = $1
	`, id).Scan(&t.ID, &t.Title, &t.Done, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *pgStore) CreateTask(ctx context.Context, title string) (*Task, error) {
	projectID, err := s.EnsureDefaultProject(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	task := &Task{
		ID:        newID(),
		Title:     title,
		Done:      false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tasks (id, project_id, title, done, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, task.ID, projectID, task.Title, task.Done, task.CreatedAt, task.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *pgStore) PatchTask(ctx context.Context, id string, title *string, done *bool) (*Task, error) {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, nil
	}
	if title != nil {
		task.Title = strings.TrimSpace(*title)
		if task.Title == "" {
			return nil, errEmptyTitle
		}
	}
	if done != nil {
		task.Done = *done
	}
	task.UpdatedAt = time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		UPDATE tasks SET title = $2, done = $3, updated_at = $4 WHERE id = $1
	`, task.ID, task.Title, task.Done, task.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *pgStore) DeleteTask(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id)
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

func (s *pgStore) UpsertUser(ctx context.Context, id, email, passwordHash, role string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	role = strings.TrimSpace(role)
	if role != "admin" && role != "member" {
		role = "member"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE
		SET role = EXCLUDED.role,
		    password_hash = EXCLUDED.password_hash
	`, id, email, passwordHash, role)
	return err
}

func (s *pgStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, role FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *pgStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, role FROM users WHERE email = $1
	`, strings.ToLower(strings.TrimSpace(email))).Scan(&u.ID, &u.Email, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *pgStore) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *pgStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, key, value)
	return err
}

func (s *pgStore) DeleteProject(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = $1`, id)
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

var (
	errNotFound   = errors.New("not found")
	errEmptyTitle = errors.New("title is required")
)
