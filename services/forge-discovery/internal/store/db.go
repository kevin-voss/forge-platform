package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB wraps a pgx pool for the discovery schema.
type DB struct {
	Pool   *pgxpool.Pool
	Schema string
}

// Open creates a pool and optionally applies embedded migrations.
func Open(ctx context.Context, databaseURL, schema string, poolMax int, migrate bool) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if poolMax > 0 {
		cfg.MaxConns = int32(poolMax)
	}
	cfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{Pool: pool, Schema: schema}
	if migrate {
		if err := db.Migrate(ctx); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return db, nil
}

// Close releases the pool.
func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

// Ready reports whether the pool can serve queries.
func (db *DB) Ready(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("database not open")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return db.Pool.Ping(pingCtx)
}

// Migrate applies embedded SQL files in lexical order (idempotent via IF NOT EXISTS).
func (db *DB) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	if _, err := db.Pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS discovery.schema_migrations (
  filename TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		// Schema may not exist yet; V01 creates it. Create schema first.
		if _, err2 := db.Pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS discovery"); err2 != nil {
			return fmt.Errorf("create schema: %w", err2)
		}
		if _, err = db.Pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS discovery.schema_migrations (
  filename TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
	}

	for _, name := range names {
		var exists bool
		if err := db.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM discovery.schema_migrations WHERE filename = $1)`, name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO discovery.schema_migrations (filename) VALUES ($1)`, name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
