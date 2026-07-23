package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapTimer tracks a per-node phase deadline.
type BootstrapTimer struct {
	NodeID         string
	Phase          string
	StartedAt      time.Time
	DeadlineAt     time.Time
	DrainStartedAt *time.Time
	UpdatedAt      time.Time
	TimeoutFired   bool // in-memory only; prevents re-fire across reconciles
}

// TimerStore persists node bootstrap deadlines.
type TimerStore interface {
	Upsert(ctx context.Context, t BootstrapTimer) error
	Get(ctx context.Context, nodeID string) (*BootstrapTimer, error)
	Clear(ctx context.Context, nodeID string) error
	MarkDrainStarted(ctx context.Context, nodeID string, at time.Time) error
	MarkTimeoutFired(ctx context.Context, nodeID string) error
}

// MemoryTimers is an in-process TimerStore for tests.
type MemoryTimers struct {
	mu   sync.Mutex
	rows map[string]BootstrapTimer
}

// NewMemoryTimers returns an empty memory timer store.
func NewMemoryTimers() *MemoryTimers {
	return &MemoryTimers{rows: map[string]BootstrapTimer{}}
}

func (m *MemoryTimers) Upsert(ctx context.Context, t BootstrapTimer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.rows[t.NodeID]; ok {
		t.TimeoutFired = existing.TimeoutFired
		if t.DrainStartedAt == nil {
			t.DrainStartedAt = existing.DrainStartedAt
		}
	}
	t.UpdatedAt = time.Now().UTC()
	if t.StartedAt.IsZero() {
		t.StartedAt = t.UpdatedAt
	}
	m.rows[t.NodeID] = t
	return nil
}

func (m *MemoryTimers) Get(ctx context.Context, nodeID string) (*BootstrapTimer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[nodeID]
	if !ok {
		return nil, nil
	}
	cp := t
	return &cp, nil
}

func (m *MemoryTimers) Clear(ctx context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, nodeID)
	return nil
}

func (m *MemoryTimers) MarkDrainStarted(ctx context.Context, nodeID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[nodeID]
	if !ok {
		t = BootstrapTimer{NodeID: nodeID, Phase: PhaseDraining, StartedAt: at}
	}
	if t.DrainStartedAt == nil {
		t.DrainStartedAt = &at
	}
	t.UpdatedAt = at
	m.rows[nodeID] = t
	return nil
}

func (m *MemoryTimers) MarkTimeoutFired(ctx context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.rows[nodeID]
	if !ok {
		return nil
	}
	t.TimeoutFired = true
	m.rows[nodeID] = t
	return nil
}

// PGTimers stores deadlines in infrastructure.node_bootstrap_timers.
type PGTimers struct {
	Pool   *pgxpool.Pool
	Schema string
}

func (p *PGTimers) table() string {
	schema := p.Schema
	if schema == "" {
		schema = "infrastructure"
	}
	return schema + ".node_bootstrap_timers"
}

func (p *PGTimers) Upsert(ctx context.Context, t BootstrapTimer) error {
	now := time.Now().UTC()
	if t.StartedAt.IsZero() {
		t.StartedAt = now
	}
	t.UpdatedAt = now
	_, err := p.Pool.Exec(ctx, fmt.Sprintf(`
INSERT INTO %s (node_id, phase, started_at, deadline_at, drain_started_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (node_id) DO UPDATE SET
  phase = EXCLUDED.phase,
  started_at = EXCLUDED.started_at,
  deadline_at = EXCLUDED.deadline_at,
  drain_started_at = COALESCE(EXCLUDED.drain_started_at, %s.drain_started_at),
  updated_at = EXCLUDED.updated_at
`, p.table(), p.table()),
		t.NodeID, t.Phase, t.StartedAt, t.DeadlineAt, t.DrainStartedAt, t.UpdatedAt,
	)
	return err
}

func (p *PGTimers) Get(ctx context.Context, nodeID string) (*BootstrapTimer, error) {
	row := p.Pool.QueryRow(ctx, fmt.Sprintf(`
SELECT node_id, phase, started_at, deadline_at, drain_started_at, updated_at
FROM %s WHERE node_id = $1
`, p.table()), nodeID)
	var t BootstrapTimer
	var drain *time.Time
	err := row.Scan(&t.NodeID, &t.Phase, &t.StartedAt, &t.DeadlineAt, &drain, &t.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t.DrainStartedAt = drain
	return &t, nil
}

func (p *PGTimers) Clear(ctx context.Context, nodeID string) error {
	_, err := p.Pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE node_id = $1`, p.table()), nodeID)
	return err
}

func (p *PGTimers) MarkDrainStarted(ctx context.Context, nodeID string, at time.Time) error {
	_, err := p.Pool.Exec(ctx, fmt.Sprintf(`
INSERT INTO %s (node_id, phase, started_at, deadline_at, drain_started_at, updated_at)
VALUES ($1, $2, $3, $3, $3, $3)
ON CONFLICT (node_id) DO UPDATE SET
  drain_started_at = COALESCE(%s.drain_started_at, EXCLUDED.drain_started_at),
  updated_at = EXCLUDED.updated_at
`, p.table(), p.table()), nodeID, PhaseDraining, at)
	return err
}

func (p *PGTimers) MarkTimeoutFired(ctx context.Context, nodeID string) error {
	// Persisted via phase transition to Failed/Draining; no extra column.
	return nil
}
