package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when an endpoint row is missing.
var ErrNotFound = errors.New("endpoint not found")

// RegisterInput is the body of an endpoint registration.
type RegisterInput struct {
	ID           string
	Project      string
	Environment  string
	Service      string
	NodeID       string
	AddressIP    string
	AddressPort  int
	Protocol     string
	Revision     string
	LeaseSeconds int
	Now          time.Time
}

// RenewInput updates lease + readiness for an existing endpoint.
type RenewInput struct {
	Project      string
	Environment  string
	ID           string
	Ready        bool
	LeaseSeconds int
	Now          time.Time
}

// EndpointRow is a persisted discovery.endpoints row.
type EndpointRow struct {
	ID              string
	Project         string
	Environment     string
	Service         string
	NodeID          string
	AddressIP       string
	AddressPort     int
	Protocol        string
	Revision        string
	Phase           string
	Ready           bool
	LeaseSeconds    int
	ExpiresAt       time.Time
	UnreadyReason   *string
	ResourceVersion string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Register upserts a service (auto-vivify) and endpoint by replica id.
// Identical re-registration leaves resource_version unchanged.
func (db *DB) Register(ctx context.Context, in RegisterInput) (EndpointRow, error) {
	if in.Protocol == "" {
		in.Protocol = "http"
	}
	if in.LeaseSeconds <= 0 {
		in.LeaseSeconds = 20
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	expires := now.Add(time.Duration(in.LeaseSeconds) * time.Second)

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return EndpointRow{}, fmt.Errorf("begin register: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO discovery.services (id, project, environment, name, ports, aliases, resource_version, created_at, updated_at)
VALUES ($1, $2, $3, $4, '[]'::jsonb, '[]'::jsonb, '1', $5, $5)
ON CONFLICT (project, environment, name) DO NOTHING
`, "svc_"+in.Project+"_"+in.Environment+"_"+in.Service, in.Project, in.Environment, in.Service, now); err != nil {
		return EndpointRow{}, fmt.Errorf("vivify service: %w", err)
	}

	var existing EndpointRow
	err = scanEndpoint(tx.QueryRow(ctx, `
SELECT id, project, environment, service, node_id, address_ip, address_port, protocol,
       COALESCE(revision, ''), phase, ready, lease_seconds, expires_at, unready_reason,
       resource_version, created_at, updated_at
  FROM discovery.endpoints
 WHERE id = $1
`, in.ID), &existing)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		rv := "1"
		_, err = tx.Exec(ctx, `
INSERT INTO discovery.endpoints (
  id, project, environment, service, node_id, address_ip, address_port, protocol, revision,
  phase, ready, lease_seconds, expires_at, unready_reason, resource_version, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),'Pending',false,$10,$11,NULL,$12,$13,$13
)`,
			in.ID, in.Project, in.Environment, in.Service, in.NodeID, in.AddressIP, in.AddressPort,
			in.Protocol, in.Revision, in.LeaseSeconds, expires, rv, now,
		)
		if err != nil {
			return EndpointRow{}, fmt.Errorf("insert endpoint: %w", err)
		}
	case err != nil:
		return EndpointRow{}, fmt.Errorf("select endpoint: %w", err)
	default:
		identical := existing.Project == in.Project &&
			existing.Environment == in.Environment &&
			existing.Service == in.Service &&
			existing.NodeID == in.NodeID &&
			existing.AddressIP == in.AddressIP &&
			existing.AddressPort == in.AddressPort &&
			existing.Protocol == in.Protocol &&
			existing.Revision == in.Revision &&
			existing.LeaseSeconds == in.LeaseSeconds
		if identical {
			// Refresh lease clock only; keep resource_version and phase.
			_, err = tx.Exec(ctx, `
UPDATE discovery.endpoints
   SET expires_at = $2, updated_at = $3
 WHERE id = $1
`, in.ID, expires, now)
			if err != nil {
				return EndpointRow{}, fmt.Errorf("refresh identical endpoint: %w", err)
			}
		} else {
			nextRV := bumpResourceVersion(existing.ResourceVersion)
			_, err = tx.Exec(ctx, `
UPDATE discovery.endpoints
   SET project = $2, environment = $3, service = $4, node_id = $5,
       address_ip = $6, address_port = $7, protocol = $8, revision = NULLIF($9,''),
       lease_seconds = $10, expires_at = $11, phase = 'Pending', ready = false,
       unready_reason = NULL, resource_version = $12, updated_at = $13
 WHERE id = $1
`,
				in.ID, in.Project, in.Environment, in.Service, in.NodeID, in.AddressIP, in.AddressPort,
				in.Protocol, in.Revision, in.LeaseSeconds, expires, nextRV, now,
			)
			if err != nil {
				return EndpointRow{}, fmt.Errorf("update endpoint: %w", err)
			}
		}
	}

	row, err := getEndpointTx(ctx, tx, in.ID)
	if err != nil {
		return EndpointRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointRow{}, fmt.Errorf("commit register: %w", err)
	}
	return row, nil
}

// Renew resets expires_at and flips phase based on ready.
func (db *DB) Renew(ctx context.Context, in RenewInput) (EndpointRow, error) {
	if in.LeaseSeconds <= 0 {
		in.LeaseSeconds = 20
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	expires := now.Add(time.Duration(in.LeaseSeconds) * time.Second)
	phase := "Ready"
	var reason *string
	if !in.Ready {
		phase = "Unready"
		r := "NotReady"
		reason = &r
	}

	tag, err := db.Pool.Exec(ctx, `
UPDATE discovery.endpoints
   SET ready = $4, lease_seconds = $5, expires_at = $6, phase = $7,
       unready_reason = $8, resource_version = (COALESCE(NULLIF(resource_version, ''), '0')::bigint + 1)::text,
       updated_at = $9
 WHERE id = $1 AND project = $2 AND environment = $3
`, in.ID, in.Project, in.Environment, in.Ready, in.LeaseSeconds, expires, phase, reason, now)
	if err != nil {
		return EndpointRow{}, fmt.Errorf("renew: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return EndpointRow{}, ErrNotFound
	}
	return db.GetEndpoint(ctx, in.Project, in.Environment, in.ID)
}

// Deregister removes an endpoint immediately.
func (db *DB) Deregister(ctx context.Context, project, environment, id string) error {
	tag, err := db.Pool.Exec(ctx, `
DELETE FROM discovery.endpoints
 WHERE id = $1 AND project = $2 AND environment = $3
`, id, project, environment)
	if err != nil {
		return fmt.Errorf("deregister: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetEndpoint loads one endpoint by scope + id.
func (db *DB) GetEndpoint(ctx context.Context, project, environment, id string) (EndpointRow, error) {
	var row EndpointRow
	err := scanEndpoint(db.Pool.QueryRow(ctx, `
SELECT id, project, environment, service, node_id, address_ip, address_port, protocol,
       COALESCE(revision, ''), phase, ready, lease_seconds, expires_at, unready_reason,
       resource_version, created_at, updated_at
  FROM discovery.endpoints
 WHERE id = $1 AND project = $2 AND environment = $3
`, id, project, environment), &row)
	if errors.Is(err, pgx.ErrNoRows) {
		return EndpointRow{}, ErrNotFound
	}
	if err != nil {
		return EndpointRow{}, err
	}
	return row, nil
}

// ListServiceEndpoints returns all endpoints for a service (unfiltered).
func (db *DB) ListServiceEndpoints(ctx context.Context, project, environment, service string) ([]EndpointRow, error) {
	rows, err := db.Pool.Query(ctx, `
SELECT id, project, environment, service, node_id, address_ip, address_port, protocol,
       COALESCE(revision, ''), phase, ready, lease_seconds, expires_at, unready_reason,
       resource_version, created_at, updated_at
  FROM discovery.endpoints
 WHERE project = $1 AND environment = $2 AND service = $3
 ORDER BY id
`, project, environment, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndpointRow
	for rows.Next() {
		var row EndpointRow
		if err := scanEndpoint(rows, &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ExpireLeases marks expired non-Unready endpoints as Unready (LeaseExpired).
// Returns ids that transitioned.
func (db *DB) ExpireLeases(ctx context.Context, now time.Time) ([]string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	rows, err := db.Pool.Query(ctx, `
UPDATE discovery.endpoints
   SET phase = 'Unready', ready = false, unready_reason = 'LeaseExpired',
       resource_version = (COALESCE(NULLIF(resource_version, ''), '0')::bigint + 1)::text,
       updated_at = $1
 WHERE expires_at < $1 AND phase <> 'Unready'
 RETURNING id
`, now)
	if err != nil {
		return nil, fmt.Errorf("expire leases: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReapUnready deletes Unready endpoints whose updated_at is older than cutoff.
func (db *DB) ReapUnready(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := db.Pool.Exec(ctx, `
DELETE FROM discovery.endpoints
 WHERE phase = 'Unready' AND updated_at < $1
`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("reap unready: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkNodeUnready marks every non-Unready endpoint on a node Unready in one transaction.
func (db *DB) MarkNodeUnready(ctx context.Context, nodeID string, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
UPDATE discovery.endpoints
   SET phase = 'Unready', ready = false, unready_reason = 'NodeUnreachable',
       resource_version = (COALESCE(NULLIF(resource_version, ''), '0')::bigint + 1)::text,
       updated_at = $2
 WHERE node_id = $1 AND phase <> 'Unready'
`, nodeID, now)
	if err != nil {
		return 0, fmt.Errorf("mark node unready: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountByPhase returns endpoint counts keyed by phase.
func (db *DB) CountByPhase(ctx context.Context) (map[string]int64, error) {
	rows, err := db.Pool.Query(ctx, `
SELECT phase, COUNT(*) FROM discovery.endpoints GROUP BY phase
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var phase string
		var n int64
		if err := rows.Scan(&phase, &n); err != nil {
			return nil, err
		}
		out[phase] = n
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanEndpoint(row scannable, dest *EndpointRow) error {
	return row.Scan(
		&dest.ID, &dest.Project, &dest.Environment, &dest.Service, &dest.NodeID,
		&dest.AddressIP, &dest.AddressPort, &dest.Protocol, &dest.Revision, &dest.Phase,
		&dest.Ready, &dest.LeaseSeconds, &dest.ExpiresAt, &dest.UnreadyReason,
		&dest.ResourceVersion, &dest.CreatedAt, &dest.UpdatedAt,
	)
}

func getEndpointTx(ctx context.Context, tx pgx.Tx, id string) (EndpointRow, error) {
	var row EndpointRow
	err := scanEndpoint(tx.QueryRow(ctx, `
SELECT id, project, environment, service, node_id, address_ip, address_port, protocol,
       COALESCE(revision, ''), phase, ready, lease_seconds, expires_at, unready_reason,
       resource_version, created_at, updated_at
  FROM discovery.endpoints
 WHERE id = $1
`, id), &row)
	if errors.Is(err, pgx.ErrNoRows) {
		return EndpointRow{}, ErrNotFound
	}
	return row, err
}

func bumpResourceVersion(current string) string {
	n, err := strconv.ParseInt(current, 10, 64)
	if err != nil || n < 0 {
		return "1"
	}
	return strconv.FormatInt(n+1, 10)
}
