package inventory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore persists claims in infrastructure.ssh_inventory_claims.
type PGStore struct {
	Pool   *pgxpool.Pool
	Schema string
}

// NewPGStore constructs a Postgres-backed Store.
func NewPGStore(pool *pgxpool.Pool, schema string) *PGStore {
	if schema == "" {
		schema = "infrastructure"
	}
	return &PGStore{Pool: pool, Schema: schema}
}

func (s *PGStore) table() string {
	return quoteIdent(s.Schema) + ".ssh_inventory_claims"
}

func (s *PGStore) EnsureHosts(ctx context.Context, providerName string, addresses []string) error {
	for _, addr := range addresses {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		_, err := s.Pool.Exec(ctx, fmt.Sprintf(`
INSERT INTO %s (provider_name, address)
VALUES ($1, $2)
ON CONFLICT (provider_name, address) DO NOTHING
`, s.table()), providerName, addr)
		if err != nil {
			return fmt.Errorf("ensure host %s: %w", addr, err)
		}
	}
	return nil
}

func (s *PGStore) ClaimNext(ctx context.Context, providerName, poolName string, candidates []string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var row pgx.Row
	if len(candidates) == 0 {
		row = tx.QueryRow(ctx, fmt.Sprintf(`
UPDATE %s
SET claimed_by_pool = $1, claimed_at = now()
WHERE provider_name = $2 AND address = (
  SELECT address FROM %s
  WHERE provider_name = $2 AND claimed_by_pool IS NULL
  ORDER BY address
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING address
`, s.table(), s.table()), poolName, providerName)
	} else {
		row = tx.QueryRow(ctx, fmt.Sprintf(`
UPDATE %s
SET claimed_by_pool = $1, claimed_at = now()
WHERE provider_name = $2 AND address = (
  SELECT address FROM %s
  WHERE provider_name = $2 AND claimed_by_pool IS NULL AND address = ANY($3)
  ORDER BY address
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING address
`, s.table(), s.table()), poolName, providerName, candidates)
	}

	var address string
	if err := row.Scan(&address); err != nil {
		if err == pgx.ErrNoRows {
			return "", ErrNoFreeHost
		}
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return address, nil
}

func (s *PGStore) Release(ctx context.Context, providerName, address string) error {
	_, err := s.Pool.Exec(ctx, fmt.Sprintf(`
UPDATE %s
SET claimed_by_pool = NULL, claimed_at = NULL
WHERE provider_name = $1 AND address = $2
`, s.table()), providerName, address)
	return err
}

func (s *PGStore) Get(ctx context.Context, providerName, address string) (*Claim, error) {
	var c Claim
	var pool *string
	var at *time.Time
	err := s.Pool.QueryRow(ctx, fmt.Sprintf(`
SELECT provider_name, address, claimed_by_pool, claimed_at
FROM %s WHERE provider_name = $1 AND address = $2
`, s.table()), providerName, address).Scan(&c.ProviderName, &c.Address, &pool, &at)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pool != nil {
		c.ClaimedByPool = *pool
	}
	c.ClaimedAt = at
	return &c, nil
}

func (s *PGStore) List(ctx context.Context, providerName string) ([]Claim, error) {
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
SELECT provider_name, address, claimed_by_pool, claimed_at
FROM %s WHERE provider_name = $1 ORDER BY address
`, s.table()), providerName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Claim
	for rows.Next() {
		var c Claim
		var pool *string
		var at *time.Time
		if err := rows.Scan(&c.ProviderName, &c.Address, &pool, &at); err != nil {
			return nil, err
		}
		if pool != nil {
			c.ClaimedByPool = *pool
		}
		c.ClaimedAt = at
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) CountClaimed(ctx context.Context, providerName string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, fmt.Sprintf(`
SELECT COUNT(*) FROM %s WHERE provider_name = $1 AND claimed_by_pool IS NOT NULL
`, s.table()), providerName).Scan(&n)
	return n, err
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
