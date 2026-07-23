package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Metrics for publish/consumer dedup and processed marks.
type Metrics struct {
	PublishDedupHits atomic.Uint64
	ConsumerDedupSkips atomic.Uint64
	ProcessedEvents    atomic.Uint64
}

// SeenStore tracks per-consumer processed event ids for redelivery dedup.
type SeenStore interface {
	Mark(ctx context.Context, consumer, eventID string) error
	IsProcessed(ctx context.Context, consumer, eventID string) (bool, error)
	Cleanup(ctx context.Context, olderThan time.Duration) (int64, error)
}

// MemoryStore is an in-process seen store (tests / DB-less local runs).
type MemoryStore struct {
	mu   sync.Mutex
	seen map[string]time.Time // key = consumer\x00event_id
}

// NewMemoryStore constructs an empty in-memory seen store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{seen: make(map[string]time.Time)}
}

func memKey(consumer, eventID string) string {
	return consumer + "\x00" + eventID
}

// Mark records a processed event. Duplicate marks are idempotent.
func (s *MemoryStore) Mark(_ context.Context, consumer, eventID string) error {
	if err := validatePair(consumer, eventID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[memKey(consumer, eventID)] = time.Now().UTC()
	return nil
}

// IsProcessed reports whether the consumer already marked the event.
func (s *MemoryStore) IsProcessed(_ context.Context, consumer, eventID string) (bool, error) {
	if err := validatePair(consumer, eventID); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[memKey(consumer, eventID)]
	return ok, nil
}

// Cleanup removes marks older than the retention window.
func (s *MemoryStore) Cleanup(_ context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-olderThan)
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for k, at := range s.seen {
		if at.Before(cutoff) {
			delete(s.seen, k)
			n++
		}
	}
	return n, nil
}

// PostgresStore persists processed_events in Postgres.
type PostgresStore struct {
	db *sql.DB
}

// OpenPostgres opens a Postgres-backed seen store and migrates schema.
func OpenPostgres(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS processed_events (
  consumer   TEXT NOT NULL,
  event_id   TEXT NOT NULL,
  at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer, event_id)
);
CREATE INDEX IF NOT EXISTS idx_processed_at ON processed_events(at);
`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("migrate processed_events: %w", err)
	}
	return nil
}

// Close closes the underlying DB pool.
func (s *PostgresStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Mark inserts a processed marker. PK conflicts are treated as success.
func (s *PostgresStore) Mark(ctx context.Context, consumer, eventID string) error {
	if err := validatePair(consumer, eventID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2)
		 ON CONFLICT (consumer, event_id) DO NOTHING`,
		consumer, eventID,
	)
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}

// IsProcessed reports whether a marker exists.
func (s *PostgresStore) IsProcessed(ctx context.Context, consumer, eventID string) (bool, error) {
	if err := validatePair(consumer, eventID); err != nil {
		return false, err
	}
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM processed_events WHERE consumer = $1 AND event_id = $2`,
		consumer, eventID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is processed: %w", err)
	}
	return true, nil
}

// Cleanup deletes markers older than the retention window.
func (s *PostgresStore) Cleanup(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-olderThan)
	res, err := s.db.ExecContext(ctx, `DELETE FROM processed_events WHERE at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup processed: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RetentionRunner periodically purges expired processed markers.
type RetentionRunner struct {
	store SeenStore
	ttl   time.Duration
	log   *slog.Logger
	every time.Duration
}

// NewRetentionRunner builds a background cleanup loop.
func NewRetentionRunner(store SeenStore, ttlS int, log *slog.Logger) *RetentionRunner {
	if log == nil {
		log = slog.Default()
	}
	ttl := time.Duration(ttlS) * time.Second
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &RetentionRunner{
		store: store,
		ttl:   ttl,
		log:   log,
		every: time.Hour,
	}
}

// Run blocks until ctx is cancelled, purging on an interval.
func (r *RetentionRunner) Run(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	t := time.NewTicker(r.every)
	defer t.Stop()
	r.purge(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.purge(ctx)
		}
	}
}

func (r *RetentionRunner) purge(ctx context.Context) {
	n, err := r.store.Cleanup(ctx, r.ttl)
	if err != nil {
		r.log.Warn("processed_events cleanup failed", "error", err.Error())
		return
	}
	if n > 0 {
		r.log.Info("processed_events cleanup", "deleted", n, "ttl_s", int(r.ttl.Seconds()))
	}
}

func validatePair(consumer, eventID string) error {
	if strings.TrimSpace(consumer) == "" {
		return fmt.Errorf("consumer is required")
	}
	if strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("event_id is required")
	}
	return nil
}
