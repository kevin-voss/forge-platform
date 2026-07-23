package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Operation statuses.
const (
	StatusPending   = "pending"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Operation kinds (mutating provider calls).
const (
	KindCreateNode     = "create_node"
	KindDeleteNode     = "delete_node"
	KindRebootNode     = "reboot_node"
	KindCreateNetwork  = "create_network"
	KindDeleteNetwork  = "delete_network"
	KindAttachDisk     = "attach_disk"
	KindDetachDisk     = "detach_disk"
	KindResizeDisk     = "resize_disk"
	KindCreatePublicIP = "create_public_ip"
	KindDeletePublicIP = "delete_public_ip"
)

// Target kinds for ledger rows.
const (
	TargetNodePool = "node_pool"
	TargetNode     = "node"
	TargetNetwork  = "network"
	TargetDisk     = "disk"
	TargetPublicIP = "public_ip"
)

// ErrDuplicatePending indicates Begin found an existing pending row for the natural key.
var ErrDuplicatePending = errors.New("operation already pending for natural key")

// Operation is a provider_operations row.
type Operation struct {
	ID           string
	ProviderName string
	Kind         string
	TargetKind   string
	TargetID     *string
	NaturalKey   string
	Request      json.RawMessage
	Status       string
	Result       json.RawMessage
	Error        *string
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// BeginResult is returned by Ledger.Begin.
type BeginResult struct {
	Op            *Operation
	AlreadyExists bool // true when a row for (provider, natural_key) already existed
	SkipProvider  bool // true for pending or succeeded — do not call the provider again
}

// Ledger persists operation ids before mutating provider calls.
type Ledger struct {
	Pool   *pgxpool.Pool
	IDs    *Generator
	Schema string
}

func (l *Ledger) table() string {
	schema := l.Schema
	if schema == "" {
		schema = "infrastructure"
	}
	return schema + ".provider_operations"
}

// Begin inserts a pending row or returns the existing row for (provider, natural_key).
func (l *Ledger) Begin(ctx context.Context, providerName, kind, targetKind, naturalKey string, request any) (*BeginResult, error) {
	if existing, err := l.FindByNaturalKey(ctx, providerName, naturalKey); err != nil {
		return nil, err
	} else if existing != nil {
		skip := existing.Status == StatusPending || existing.Status == StatusSucceeded
		return &BeginResult{Op: existing, AlreadyExists: true, SkipProvider: skip}, nil
	}

	reqJSON, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	id := l.IDs.NewOpID()
	row := &Operation{
		ID:           id,
		ProviderName: providerName,
		Kind:         kind,
		TargetKind:   targetKind,
		NaturalKey:   naturalKey,
		Request:      reqJSON,
		Status:       StatusPending,
		CreatedAt:    time.Now().UTC(),
	}

	_, err = l.Pool.Exec(ctx, fmt.Sprintf(`
INSERT INTO %s (id, provider_name, kind, target_kind, natural_key, request, status)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
`, l.table()),
		row.ID, row.ProviderName, row.Kind, row.TargetKind, row.NaturalKey, string(reqJSON), row.Status,
	)
	if err != nil {
		// Race: another instance inserted first — return that row.
		existing, findErr := l.FindByNaturalKey(ctx, providerName, naturalKey)
		if findErr != nil {
			return nil, fmt.Errorf("insert operation: %w", err)
		}
		if existing != nil {
			skip := existing.Status == StatusPending || existing.Status == StatusSucceeded
			return &BeginResult{Op: existing, AlreadyExists: true, SkipProvider: skip}, nil
		}
		return nil, fmt.Errorf("insert operation: %w", err)
	}
	return &BeginResult{Op: row, AlreadyExists: false, SkipProvider: false}, nil
}

// Complete marks an operation succeeded or failed.
func (l *Ledger) Complete(ctx context.Context, opID string, result any, callErr error) error {
	status := StatusSucceeded
	var errText *string
	if callErr != nil {
		status = StatusFailed
		msg := callErr.Error()
		errText = &msg
	}
	var resultJSON []byte
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		resultJSON = b
	}
	tag, err := l.Pool.Exec(ctx, fmt.Sprintf(`
UPDATE %s
SET status = $2,
    result = $3::jsonb,
    error = $4,
    completed_at = now(),
    target_id = COALESCE(target_id, $5)
WHERE id = $1
`, l.table()),
		opID, status, nullableJSON(resultJSON), errText, targetIDFromResult(result),
	)
	if err != nil {
		return fmt.Errorf("complete operation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("operation %s not found", opID)
	}
	return nil
}

// FindByNaturalKey returns the row for (provider, natural_key), or nil.
func (l *Ledger) FindByNaturalKey(ctx context.Context, providerName, naturalKey string) (*Operation, error) {
	row := &Operation{}
	var targetID, errText *string
	var result []byte
	var completedAt *time.Time
	err := l.Pool.QueryRow(ctx, fmt.Sprintf(`
SELECT id, provider_name, kind, target_kind, target_id, natural_key, request, status, result, error, created_at, completed_at
FROM %s
WHERE provider_name = $1 AND natural_key = $2
`, l.table()), providerName, naturalKey).Scan(
		&row.ID, &row.ProviderName, &row.Kind, &row.TargetKind, &targetID, &row.NaturalKey,
		&row.Request, &row.Status, &result, &errText, &row.CreatedAt, &completedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find by natural key: %w", err)
	}
	row.TargetID = targetID
	row.Result = result
	row.Error = errText
	row.CompletedAt = completedAt
	return row, nil
}

// Get returns an operation by id.
func (l *Ledger) Get(ctx context.Context, opID string) (*Operation, error) {
	row := &Operation{}
	var targetID, errText *string
	var result []byte
	var completedAt *time.Time
	err := l.Pool.QueryRow(ctx, fmt.Sprintf(`
SELECT id, provider_name, kind, target_kind, target_id, natural_key, request, status, result, error, created_at, completed_at
FROM %s
WHERE id = $1
`, l.table()), opID).Scan(
		&row.ID, &row.ProviderName, &row.Kind, &row.TargetKind, &targetID, &row.NaturalKey,
		&row.Request, &row.Status, &result, &errText, &row.CreatedAt, &completedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get operation: %w", err)
	}
	row.TargetID = targetID
	row.Result = result
	row.Error = errText
	row.CompletedAt = completedAt
	return row, nil
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func targetIDFromResult(result any) any {
	switch v := result.(type) {
	case map[string]any:
		if id, ok := v["id"].(string); ok {
			return id
		}
	case interface{ GetID() string }:
		return v.GetID()
	}
	// Best-effort via JSON.
	b, err := json.Marshal(result)
	if err != nil || len(b) == 0 || string(b) == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) == nil {
		if id, ok := m["id"].(string); ok {
			return id
		}
	}
	return nil
}
