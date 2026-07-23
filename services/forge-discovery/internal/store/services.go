package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ServiceRow is a persisted discovery.services row.
type ServiceRow struct {
	ID              string
	Project         string
	Environment     string
	Name            string
	Ports           []ServicePortRow
	Aliases         []string
	ResourceVersion string
}

// ServicePortRow is one named port from services.ports JSONB.
type ServicePortRow struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// ListServices returns every service row (canonical name + aliases) for Gateway sync.
func (db *DB) ListServices(ctx context.Context) ([]ServiceRow, error) {
	rows, err := db.Pool.Query(ctx, `
SELECT id, project, environment, name, ports, aliases, resource_version
  FROM discovery.services
 ORDER BY project, environment, name
`)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	out := make([]ServiceRow, 0)
	for rows.Next() {
		var row ServiceRow
		var portsRaw, aliasesRaw []byte
		if err := rows.Scan(
			&row.ID, &row.Project, &row.Environment, &row.Name, &portsRaw, &aliasesRaw, &row.ResourceVersion,
		); err != nil {
			return nil, fmt.Errorf("list services scan: %w", err)
		}
		if err := json.Unmarshal(portsRaw, &row.Ports); err != nil {
			row.Ports = nil
		}
		if err := json.Unmarshal(aliasesRaw, &row.Aliases); err != nil {
			row.Aliases = nil
		}
		if row.Aliases == nil {
			row.Aliases = []string{}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	return out, nil
}

// LookupServiceByNameOrAlias finds a service by canonical name or alias within a scope.
func (db *DB) LookupServiceByNameOrAlias(ctx context.Context, project, environment, nameOrAlias string) (ServiceRow, error) {
	var row ServiceRow
	var portsRaw, aliasesRaw []byte
	err := db.Pool.QueryRow(ctx, `
SELECT id, project, environment, name, ports, aliases, resource_version
  FROM discovery.services
 WHERE project = $1 AND environment = $2
   AND (name = $3 OR aliases @> to_jsonb($3::text))
 LIMIT 1
`, project, environment, nameOrAlias).Scan(
		&row.ID, &row.Project, &row.Environment, &row.Name, &portsRaw, &aliasesRaw, &row.ResourceVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceRow{}, ErrNotFound
	}
	if err != nil {
		return ServiceRow{}, fmt.Errorf("lookup service: %w", err)
	}
	if err := json.Unmarshal(portsRaw, &row.Ports); err != nil {
		row.Ports = nil
	}
	if err := json.Unmarshal(aliasesRaw, &row.Aliases); err != nil {
		row.Aliases = nil
	}
	return row, nil
}

// SetServiceAliases updates aliases for an existing service (test/helper).
func (db *DB) SetServiceAliases(ctx context.Context, project, environment, name string, aliases []string) error {
	if aliases == nil {
		aliases = []string{}
	}
	raw, err := json.Marshal(aliases)
	if err != nil {
		return err
	}
	tag, err := db.Pool.Exec(ctx, `
UPDATE discovery.services
   SET aliases = $4::jsonb, updated_at = now()
 WHERE project = $1 AND environment = $2 AND name = $3
`, project, environment, name, string(raw))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetServicePorts updates ports for an existing service (test/helper).
func (db *DB) SetServicePorts(ctx context.Context, project, environment, name string, ports []ServicePortRow) error {
	if ports == nil {
		ports = []ServicePortRow{}
	}
	raw, err := json.Marshal(ports)
	if err != nil {
		return err
	}
	tag, err := db.Pool.Exec(ctx, `
UPDATE discovery.services
   SET ports = $4::jsonb, updated_at = now()
 WHERE project = $1 AND environment = $2 AND name = $3
`, project, environment, name, string(raw))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
