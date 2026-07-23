package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Membership errors.
var (
	ErrNodeMembershipNotFound = errors.New("NodeMembershipNotFound")
)

// NodeMembership is the persisted per-node transport attrs.
type NodeMembership struct {
	NodeID          string    `json:"node_id"`
	Membership      *string   `json:"membership"`
	DockerColocated bool      `json:"docker_colocated"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TransportPair is GET .../transport.
type TransportPair struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Transport string `json:"transport"`
}

// TransportMetrics holds pair counters by transport.
type TransportMetrics struct {
	Docker          atomic.Int64
	ProviderPrivate atomic.Int64
	Wireguard       atomic.Int64
}

// MembershipStore persists node membership and the network_routes cache.
type MembershipStore struct {
	Pool        *pgxpool.Pool
	Log         *slog.Logger
	DefaultMode string
	Metrics     *TransportMetrics
}

func (s *MembershipStore) defaultMode() string {
	if s == nil || strings.TrimSpace(s.DefaultMode) == "" {
		return TransportWireguard
	}
	return s.DefaultMode
}

// UpsertMembership sets membership and/or docker_colocated for a node, then
// recomputes the routes cache for every network that has an active lease for it.
// Nil pointers leave the existing field unchanged (or default for a new row).
func (s *MembershipStore) UpsertMembership(ctx context.Context, nodeID string, membership *string, dockerColocated *bool) (NodeMembership, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return NodeMembership{}, fmt.Errorf("node_id is required")
	}
	if s == nil || s.Pool == nil {
		return NodeMembership{}, fmt.Errorf("membership store not configured")
	}

	cur, err := s.GetMembership(ctx, nodeID)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNodeMembershipNotFound) {
		return NodeMembership{}, err
	}

	var memArg any
	switch {
	case membership != nil:
		m := strings.TrimSpace(*membership)
		if m == "" {
			memArg = nil
		} else {
			memArg = m
		}
	case exists && cur.Membership != nil:
		memArg = *cur.Membership
	default:
		memArg = nil
	}

	coloc := false
	if exists {
		coloc = cur.DockerColocated
	}
	if dockerColocated != nil {
		coloc = *dockerColocated
	}

	row := NodeMembership{}
	err = s.Pool.QueryRow(ctx, `
INSERT INTO network.nodes (node_id, network_membership, docker_colocated, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (node_id) DO UPDATE SET
  network_membership = EXCLUDED.network_membership,
  docker_colocated   = EXCLUDED.docker_colocated,
  updated_at         = now()
RETURNING node_id, network_membership, docker_colocated, updated_at
`, nodeID, memArg, coloc).Scan(&row.NodeID, &row.Membership, &row.DockerColocated, &row.UpdatedAt)
	if err != nil {
		return NodeMembership{}, fmt.Errorf("upsert membership: %w", err)
	}

	if err := s.RecomputeAllRoutesForNode(ctx, nodeID); err != nil {
		return NodeMembership{}, err
	}
	return row, nil
}

// GetMembership returns the stored attrs for a node (defaults if missing).
func (s *MembershipStore) GetMembership(ctx context.Context, nodeID string) (NodeMembership, error) {
	if s == nil || s.Pool == nil {
		return NodeMembership{}, fmt.Errorf("membership store not configured")
	}
	var row NodeMembership
	err := s.Pool.QueryRow(ctx, `
SELECT node_id, network_membership, docker_colocated, updated_at
FROM network.nodes WHERE node_id = $1
`, nodeID).Scan(&row.NodeID, &row.Membership, &row.DockerColocated, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeMembership{}, ErrNodeMembershipNotFound
	}
	if err != nil {
		return NodeMembership{}, err
	}
	return row, nil
}

// AttrsFor returns transport attrs, defaulting missing nodes to empty/false.
func (s *MembershipStore) AttrsFor(ctx context.Context, nodeID string) (NodeTransportAttrs, error) {
	row, err := s.GetMembership(ctx, nodeID)
	if errors.Is(err, ErrNodeMembershipNotFound) {
		return NodeTransportAttrs{}, nil
	}
	if err != nil {
		return NodeTransportAttrs{}, err
	}
	mem := ""
	if row.Membership != nil {
		mem = *row.Membership
	}
	return NodeTransportAttrs{Membership: mem, DockerColocated: row.DockerColocated}, nil
}

// ResolvePair computes transport for (from, to) without requiring a cache hit.
func (s *MembershipStore) ResolvePair(ctx context.Context, from, to string) (string, error) {
	a, err := s.AttrsFor(ctx, from)
	if err != nil {
		return "", err
	}
	b, err := s.AttrsFor(ctx, to)
	if err != nil {
		return "", err
	}
	return ResolveTransport(a, b, s.defaultMode()), nil
}

// GetTransport returns the cached or freshly computed transport for a pair on a network.
func (s *MembershipStore) GetTransport(ctx context.Context, networkName, from, to string) (TransportPair, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return TransportPair{}, fmt.Errorf("from and to query params are required")
	}
	if from == to {
		return TransportPair{}, fmt.Errorf("from and to must differ")
	}

	var networkID string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM network.networks WHERE name = $1`, networkName).Scan(&networkID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TransportPair{}, ErrNetworkNotFound
	}
	if err != nil {
		return TransportPair{}, err
	}

	transport, err := s.ResolvePair(ctx, from, to)
	if err != nil {
		return TransportPair{}, err
	}

	// Refresh cache row for observability.
	if _, err := s.Pool.Exec(ctx, `
INSERT INTO network.network_routes (network_id, from_node, to_node, transport, computed_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (network_id, from_node, to_node) DO UPDATE SET
  transport   = EXCLUDED.transport,
  computed_at = now()
`, networkID, from, to, transport); err != nil {
		return TransportPair{}, fmt.Errorf("upsert route cache: %w", err)
	}

	return TransportPair{From: from, To: to, Transport: transport}, nil
}

// RecomputeAllRoutesForNode refreshes the routes cache for every network where
// the node (or any peer with an active lease) participates.
func (s *MembershipStore) RecomputeAllRoutesForNode(ctx context.Context, nodeID string) error {
	rows, err := s.Pool.Query(ctx, `
SELECT DISTINCT network_id FROM network.node_leases
WHERE released_at IS NULL AND node_id = $1
`, nodeID)
	if err != nil {
		return fmt.Errorf("list networks for node: %w", err)
	}
	defer rows.Close()
	var networkIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		networkIDs = append(networkIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Also recompute networks that already have cached routes involving this node.
	rows2, err := s.Pool.Query(ctx, `
SELECT DISTINCT network_id FROM network.network_routes
WHERE from_node = $1 OR to_node = $1
`, nodeID)
	if err != nil {
		return err
	}
	defer rows2.Close()
	seen := map[string]struct{}{}
	for _, id := range networkIDs {
		seen[id] = struct{}{}
	}
	for rows2.Next() {
		var id string
		if err := rows2.Scan(&id); err != nil {
			return err
		}
		if _, ok := seen[id]; !ok {
			networkIDs = append(networkIDs, id)
			seen[id] = struct{}{}
		}
	}
	if err := rows2.Err(); err != nil {
		return err
	}

	for _, nid := range networkIDs {
		if err := s.RecomputeRoutes(ctx, nid); err != nil {
			return err
		}
	}
	// If the node has no leases yet, still ensure a membership row exists; routes
	// will be filled when leases appear / GetTransport is called.
	return nil
}

// RecomputeRoutes rebuilds the full directed pair cache for one network.
func (s *MembershipStore) RecomputeRoutes(ctx context.Context, networkID string) error {
	rows, err := s.Pool.Query(ctx, `
SELECT node_id FROM network.node_leases
WHERE network_id = $1 AND released_at IS NULL
ORDER BY node_id
`, networkID)
	if err != nil {
		return fmt.Errorf("list leased nodes: %w", err)
	}
	defer rows.Close()
	var nodes []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		nodes = append(nodes, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	attrs := make(map[string]NodeTransportAttrs, len(nodes))
	for _, n := range nodes {
		a, err := s.AttrsFor(ctx, n)
		if err != nil {
			return err
		}
		attrs[n] = a
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM network.network_routes WHERE network_id = $1`, networkID); err != nil {
		return err
	}

	counts := map[string]int64{}
	for _, from := range nodes {
		for _, to := range nodes {
			if from == to {
				continue
			}
			transport := ResolveTransport(attrs[from], attrs[to], s.defaultMode())
			if _, err := tx.Exec(ctx, `
INSERT INTO network.network_routes (network_id, from_node, to_node, transport, computed_at)
VALUES ($1, $2, $3, $4, now())
`, networkID, from, to, transport); err != nil {
				return err
			}
			counts[transport]++
			if s.Log != nil {
				s.Log.Info("transport pair",
					"event", "network.transport.pair",
					"network_id", networkID,
					"from", from,
					"to", to,
					"transport", transport,
				)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.applyMetrics(counts)
	return nil
}

func (s *MembershipStore) applyMetrics(counts map[string]int64) {
	if s == nil || s.Metrics == nil {
		return
	}
	s.Metrics.Docker.Store(counts[TransportDocker])
	s.Metrics.ProviderPrivate.Store(counts[TransportProviderPrivate])
	s.Metrics.Wireguard.Store(counts[TransportWireguard])
}

// EnsureNodeDefaults upserts a node row with docker_colocated from env-driven callers.
func (s *MembershipStore) EnsureNodeDefaults(ctx context.Context, nodeID string, dockerColocated bool) error {
	_, err := s.UpsertMembership(ctx, nodeID, nil, &dockerColocated)
	return err
}
