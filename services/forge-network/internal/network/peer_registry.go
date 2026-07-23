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

// Peer status values.
const (
	PeerStatusActive   = "active"
	PeerStatusRotating = "rotating"
	PeerStatusRetiring = "retiring"
)

// Typed peer-registry errors.
var (
	ErrPeerNotFound      = errors.New("PeerNotFound")
	ErrPeerAlreadyExists = errors.New("PeerAlreadyExists")
	ErrInvalidPeerKey    = errors.New("InvalidPeerKey")
	ErrNotRotating       = errors.New("NotRotating")
)

// PeerRow is a wireguard_peers persistence row.
type PeerRow struct {
	NetworkID          string
	NodeID             string
	PublicKey          string
	Endpoint           *string
	Status             string
	RotatesTo          *string
	RetireOldAfter     *time.Time
	PeerSetVersion     int64
	AppliedPeerVersion int64
	Online             bool
	UpdatedAt          time.Time
	CIDR               string // joined from node_leases when available
}

// PeerEntry is one peer in a node's distributed peer set.
type PeerEntry struct {
	NodeID              string   `json:"node_id"`
	PublicKey           string   `json:"public_key"`
	Endpoint            *string  `json:"endpoint"`
	AllowedIPs          []string `json:"allowed_ips"`
	PersistentKeepalive int      `json:"persistent_keepalive"`
}

// PeerSetResponse is GET .../peers.
type PeerSetResponse struct {
	NodeID      string      `json:"node_id"`
	PeerVersion int64       `json:"peer_version"`
	MTU         int         `json:"mtu,omitempty"`
	Peers       []PeerEntry `json:"peers"`
}

// RotateKeyResult is POST .../rotate-key.
type RotateKeyResult struct {
	Status         string    `json:"status"`
	RetireOldAfter time.Time `json:"retire_old_after"`
}

// PeerMetrics holds in-process counters for observability (22.03).
type PeerMetrics struct {
	PeersTotal atomic.Int64
	DriftTotal atomic.Int64
}

// PeerRegistry owns node id ↔ public key ↔ endpoint registration.
type PeerRegistry struct {
	Pool             *pgxpool.Pool
	Log              *slog.Logger
	KeepaliveSeconds int
	MTU              int
	RotationWindow   time.Duration
	Metrics          *PeerMetrics
	Topology         string // mesh|hub (hub documented only)
}

func (r *PeerRegistry) keepalive() int {
	if r.KeepaliveSeconds <= 0 {
		return 25
	}
	return r.KeepaliveSeconds
}

func (r *PeerRegistry) mtu() int {
	if r.MTU <= 0 {
		return 1420
	}
	return r.MTU
}

func (r *PeerRegistry) rotationWindow() time.Duration {
	if r.RotationWindow <= 0 {
		return 5 * time.Minute
	}
	return r.RotationWindow
}

// Register upserts a node's public key + endpoint (join-time / Runtime register).
// Requires an active node lease so allowed_ips can be derived later.
func (r *PeerRegistry) Register(ctx context.Context, networkName, nodeID, publicKey, endpoint string) (PeerRow, error) {
	nodeID = strings.TrimSpace(nodeID)
	publicKey = strings.TrimSpace(publicKey)
	endpoint = strings.TrimSpace(endpoint)
	if nodeID == "" {
		return PeerRow{}, fmt.Errorf("node_id is required")
	}
	if publicKey == "" {
		return PeerRow{}, fmt.Errorf("%w: public_key is required", ErrInvalidPeerKey)
	}
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return PeerRow{}, err
	}
	var cidr string
	err = r.Pool.QueryRow(ctx, `
SELECT host(network(cidr)) || '/' || masklen(cidr)
FROM network.node_leases
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`,
		netRow.ID, nodeID).Scan(&cidr)
	if errors.Is(err, pgx.ErrNoRows) {
		return PeerRow{}, ErrNodeLeaseNotFound
	}
	if err != nil {
		return PeerRow{}, err
	}

	var ep any
	if endpoint != "" {
		ep = endpoint
	}
	_, err = r.Pool.Exec(ctx, `
INSERT INTO network.wireguard_peers (
  network_id, node_id, public_key, endpoint, status, online, updated_at
) VALUES ($1, $2, $3, $4, $5, TRUE, now())
ON CONFLICT (network_id, node_id) DO UPDATE
SET public_key = CASE
      WHEN network.wireguard_peers.status = 'rotating' THEN network.wireguard_peers.public_key
      ELSE EXCLUDED.public_key
    END,
    endpoint = COALESCE(EXCLUDED.endpoint, network.wireguard_peers.endpoint),
    online = TRUE,
    updated_at = now()`,
		netRow.ID, nodeID, publicKey, ep, PeerStatusActive)
	if err != nil {
		return PeerRow{}, err
	}

	row, err := r.getPeer(ctx, netRow.ID, nodeID)
	if err != nil {
		return PeerRow{}, err
	}
	row.CIDR = cidr
	r.refreshPeerGauge(ctx, netRow.ID)
	return row, nil
}

// Leave marks a peer offline and removes it from future peer sets.
func (r *PeerRegistry) Leave(ctx context.Context, networkName, nodeID string) error {
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return err
	}
	tag, err := r.Pool.Exec(ctx, `
UPDATE network.wireguard_peers
SET online = FALSE, updated_at = now()
WHERE network_id = $1 AND node_id = $2 AND online = TRUE`,
		netRow.ID, nodeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Allow leave when peer row was never registered (lease-only node).
		return nil
	}
	r.refreshPeerGauge(ctx, netRow.ID)
	return nil
}

// SetOnline toggles online for peer-set exclusion tests / Runtime heartbeat.
func (r *PeerRegistry) SetOnline(ctx context.Context, networkName, nodeID string, online bool) error {
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return err
	}
	tag, err := r.Pool.Exec(ctx, `
UPDATE network.wireguard_peers
SET online = $3, updated_at = now()
WHERE network_id = $1 AND node_id = $2`,
		netRow.ID, nodeID, online)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPeerNotFound
	}
	return nil
}

// RotateKey starts a dual-key window: old key stays valid, new key is advertised.
func (r *PeerRegistry) RotateKey(ctx context.Context, networkName, nodeID, newPublicKey string) (RotateKeyResult, error) {
	newPublicKey = strings.TrimSpace(newPublicKey)
	if newPublicKey == "" {
		return RotateKeyResult{}, fmt.Errorf("%w: new_public_key is required", ErrInvalidPeerKey)
	}
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return RotateKeyResult{}, err
	}
	row, err := r.getPeer(ctx, netRow.ID, nodeID)
	if err != nil {
		return RotateKeyResult{}, err
	}
	if row.PublicKey == newPublicKey {
		return RotateKeyResult{}, fmt.Errorf("%w: new_public_key must differ from current key", ErrInvalidPeerKey)
	}
	retireAt := time.Now().UTC().Add(r.rotationWindow())
	tag, err := r.Pool.Exec(ctx, `
UPDATE network.wireguard_peers
SET status = $3,
    rotates_to = $4,
    retire_old_after = $5,
    updated_at = now()
WHERE network_id = $1 AND node_id = $2 AND online = TRUE`,
		netRow.ID, nodeID, PeerStatusRotating, newPublicKey, retireAt)
	if err != nil {
		return RotateKeyResult{}, err
	}
	if tag.RowsAffected() == 0 {
		return RotateKeyResult{}, ErrPeerNotFound
	}
	if r.Log != nil {
		r.Log.Info("wireguard key rotation started",
			"event", "network.peers.rotate_start",
			"network", networkName,
			"node_id", nodeID,
			"retire_old_after", retireAt.Format(time.RFC3339),
		)
	}
	return RotateKeyResult{Status: PeerStatusRotating, RetireOldAfter: retireAt}, nil
}

// RetireOldKey completes rotation: new key becomes the sole active key.
func (r *PeerRegistry) RetireOldKey(ctx context.Context, networkName, nodeID string) error {
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return err
	}
	row, err := r.getPeer(ctx, netRow.ID, nodeID)
	if err != nil {
		return err
	}
	if row.Status != PeerStatusRotating && row.Status != PeerStatusRetiring {
		return ErrNotRotating
	}
	if row.RotatesTo == nil || *row.RotatesTo == "" {
		return fmt.Errorf("%w: rotates_to missing", ErrNotRotating)
	}
	_, err = r.Pool.Exec(ctx, `
UPDATE network.wireguard_peers
SET public_key = rotates_to,
    rotates_to = NULL,
    status = $3,
    retire_old_after = NULL,
    updated_at = now()
WHERE network_id = $1 AND node_id = $2`,
		netRow.ID, nodeID, PeerStatusActive)
	if err != nil {
		return err
	}
	if r.Log != nil {
		r.Log.Info("wireguard key rotation complete",
			"event", "network.peers.rotate_complete",
			"network", networkName,
			"node_id", nodeID,
		)
	}
	return nil
}

// ReportAppliedVersion records the peer_version Runtime has applied.
func (r *PeerRegistry) ReportAppliedVersion(ctx context.Context, networkName, nodeID string, version int64) error {
	if version < 0 {
		return fmt.Errorf("applied_peer_version must be >= 0")
	}
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return err
	}
	tag, err := r.Pool.Exec(ctx, `
UPDATE network.wireguard_peers
SET applied_peer_version = $3, updated_at = now()
WHERE network_id = $1 AND node_id = $2`,
		netRow.ID, nodeID, version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPeerNotFound
	}
	r.refreshDrift(ctx, netRow.ID)
	return nil
}

// DriftCount returns how many online nodes lag the network's current peer_version
// relative to their own peer_set_version (applied < peer_set).
func (r *PeerRegistry) DriftCount(ctx context.Context, networkName string) (int64, error) {
	netRow, err := r.networkByName(ctx, networkName)
	if err != nil {
		return 0, err
	}
	var n int64
	err = r.Pool.QueryRow(ctx, `
SELECT COUNT(*) FROM network.wireguard_peers
WHERE network_id = $1 AND online = TRUE
  AND applied_peer_version < peer_set_version`,
		netRow.ID).Scan(&n)
	return n, err
}

// ListOnline returns online peers for a network (with lease CIDRs).
func (r *PeerRegistry) ListOnline(ctx context.Context, networkID string) ([]PeerRow, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT p.network_id, p.node_id, p.public_key, p.endpoint, p.status, p.rotates_to,
       p.retire_old_after, p.peer_set_version, p.applied_peer_version, p.online, p.updated_at,
       COALESCE(host(network(l.cidr)) || '/' || masklen(l.cidr), '')
FROM network.wireguard_peers p
LEFT JOIN network.node_leases l
  ON l.network_id = p.network_id AND l.node_id = p.node_id AND l.released_at IS NULL
WHERE p.network_id = $1 AND p.online = TRUE
ORDER BY p.node_id`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PeerRow
	for rows.Next() {
		var row PeerRow
		var ep, rot *string
		var retire *time.Time
		if err := rows.Scan(
			&row.NetworkID, &row.NodeID, &row.PublicKey, &ep, &row.Status, &rot,
			&retire, &row.PeerSetVersion, &row.AppliedPeerVersion, &row.Online, &row.UpdatedAt,
			&row.CIDR,
		); err != nil {
			return nil, err
		}
		row.Endpoint = ep
		row.RotatesTo = rot
		row.RetireOldAfter = retire
		out = append(out, row)
	}
	return out, rows.Err()
}

// BumpPeerSetVersions increments peer_set_version for the given node ids and the network counter.
func (r *PeerRegistry) BumpPeerSetVersions(ctx context.Context, networkID string, nodeIDs []string) (int64, error) {
	if len(nodeIDs) == 0 {
		var v int64
		err := r.Pool.QueryRow(ctx, `SELECT peer_version FROM network.networks WHERE id = $1`, networkID).Scan(&v)
		return v, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
UPDATE network.wireguard_peers
SET peer_set_version = peer_set_version + 1, updated_at = now()
WHERE network_id = $1 AND node_id = ANY($2::text[])`,
		networkID, nodeIDs)
	if err != nil {
		return 0, err
	}
	var v int64
	err = tx.QueryRow(ctx, `
UPDATE network.networks
SET peer_version = peer_version + 1
WHERE id = $1
RETURNING peer_version`, networkID).Scan(&v)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return v, nil
}

// RetireDueRotations retires keys past retire_old_after.
func (r *PeerRegistry) RetireDueRotations(ctx context.Context) (int, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT n.name, p.node_id
FROM network.wireguard_peers p
JOIN network.networks n ON n.id = p.network_id
WHERE p.status IN ('rotating', 'retiring')
  AND p.retire_old_after IS NOT NULL
  AND p.retire_old_after <= now()
  AND p.rotates_to IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type pair struct{ net, node string }
	var due []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.net, &p.node); err != nil {
			return 0, err
		}
		due = append(due, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	retired := 0
	for _, p := range due {
		if err := r.RetireOldKey(ctx, p.net, p.node); err != nil {
			if r.Log != nil {
				r.Log.Warn("scheduled key retire failed", "network", p.net, "node_id", p.node, "error", err.Error())
			}
			continue
		}
		// Bump online peer sets so dual-key entries disappear from distribution.
		netRow, err := r.networkByName(ctx, p.net)
		if err == nil {
			online, err := r.ListOnline(ctx, netRow.ID)
			if err == nil {
				ids := make([]string, 0, len(online))
				for _, o := range online {
					ids = append(ids, o.NodeID)
				}
				_, _ = r.BumpPeerSetVersions(ctx, netRow.ID, ids)
			}
		}
		retired++
	}
	return retired, nil
}

func (r *PeerRegistry) getPeer(ctx context.Context, networkID, nodeID string) (PeerRow, error) {
	var row PeerRow
	var ep, rot *string
	var retire *time.Time
	err := r.Pool.QueryRow(ctx, `
SELECT network_id, node_id, public_key, endpoint, status, rotates_to,
       retire_old_after, peer_set_version, applied_peer_version, online, updated_at
FROM network.wireguard_peers
WHERE network_id = $1 AND node_id = $2`,
		networkID, nodeID).Scan(
		&row.NetworkID, &row.NodeID, &row.PublicKey, &ep, &row.Status, &rot,
		&retire, &row.PeerSetVersion, &row.AppliedPeerVersion, &row.Online, &row.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PeerRow{}, ErrPeerNotFound
	}
	if err != nil {
		return PeerRow{}, err
	}
	row.Endpoint = ep
	row.RotatesTo = rot
	row.RetireOldAfter = retire
	return row, nil
}

func (r *PeerRegistry) networkByName(ctx context.Context, name string) (NetworkRow, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return NetworkRow{}, ErrNetworkNotFound
	}
	var row NetworkRow
	var ipv6 *string
	var reason, message *string
	err := r.Pool.QueryRow(ctx, `
SELECT id, name, host(network(cluster_cidr)) || '/' || masklen(cluster_cidr),
       node_prefix_length, CASE WHEN ipv6_cidr IS NULL THEN NULL
         ELSE host(network(ipv6_cidr)) || '/' || masklen(ipv6_cidr) END,
       generation, resource_version, phase, condition_reason, condition_message, created_at
FROM network.networks WHERE name = $1`, name).Scan(
		&row.ID, &row.Name, &row.ClusterCIDR, &row.NodePrefixLength, &ipv6,
		&row.Generation, &row.ResourceVersion, &row.Phase, &reason, &message, &row.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NetworkRow{}, ErrNetworkNotFound
	}
	if err != nil {
		return NetworkRow{}, err
	}
	row.IPv6CIDR = ipv6
	row.ConditionReason = reason
	row.ConditionMessage = message
	return row, nil
}

func (r *PeerRegistry) refreshPeerGauge(ctx context.Context, networkID string) {
	if r.Metrics == nil {
		return
	}
	var n int64
	_ = r.Pool.QueryRow(ctx, `
SELECT COUNT(*) FROM network.wireguard_peers WHERE network_id = $1 AND online = TRUE`,
		networkID).Scan(&n)
	r.Metrics.PeersTotal.Store(n)
}

func (r *PeerRegistry) refreshDrift(ctx context.Context, networkID string) {
	if r.Metrics == nil {
		return
	}
	var n int64
	_ = r.Pool.QueryRow(ctx, `
SELECT COUNT(*) FROM network.wireguard_peers
WHERE network_id = $1 AND online = TRUE AND applied_peer_version < peer_set_version`,
		networkID).Scan(&n)
	r.Metrics.DriftTotal.Store(n)
}
