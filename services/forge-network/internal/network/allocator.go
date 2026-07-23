package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Typed allocation errors.
var (
	ErrNoAddressSpace       = errors.New("NoAddressSpaceAvailable")
	ErrNodeBlockExhausted   = errors.New("NodeBlockExhausted")
	ErrCidrCollision        = errors.New("CidrCollision")
	ErrNetworkNotFound      = errors.New("NetworkNotFound")
	ErrNetworkNotReady      = errors.New("NetworkNotReady")
	ErrNodeLeaseNotFound    = errors.New("NodeLeaseNotFound")
	ErrWorkloadLeaseMissing = errors.New("WorkloadLeaseNotFound")
)

// SubnetSource lists foreign CIDRs that must not overlap the cluster plan.
type SubnetSource interface {
	BridgeSubnets(ctx context.Context) ([]string, error)
}

// Allocator owns Network CRUD and lease allocate/release.
type Allocator struct {
	Pool          *pgxpool.Pool
	Log           *slog.Logger
	Docker        SubnetSource
	ProviderCIDRs []string
	SkipDocker    bool
}

// CheckCollision returns ErrCidrCollision when clusterCIDR overlaps Docker bridges or provider CIDRs.
func (a *Allocator) CheckCollision(ctx context.Context, clusterCIDR string) error {
	var foreign []string
	foreign = append(foreign, a.ProviderCIDRs...)
	if !a.SkipDocker && a.Docker != nil {
		bridges, err := a.Docker.BridgeSubnets(ctx)
		if err != nil {
			if a.Log != nil {
				a.Log.Warn("docker bridge subnet lookup failed; continuing with provider CIDRs only",
					"error", err.Error())
			}
		} else {
			foreign = append(foreign, bridges...)
		}
	}
	for _, f := range foreign {
		ok, err := Overlaps(clusterCIDR, f)
		if err != nil {
			continue
		}
		if ok {
			return fmt.Errorf("%w: cluster CIDR %s overlaps %s", ErrCidrCollision, clusterCIDR, f)
		}
	}
	return nil
}

// CreateNetwork inserts a Network resource after collision checks.
func (a *Allocator) CreateNetwork(ctx context.Context, name, clusterCIDR string, nodePrefix int, ipv6 *string) (NetworkRow, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return NetworkRow{}, fmt.Errorf("name is required")
	}
	if _, err := ParsePlan(clusterCIDR, nodePrefix); err != nil {
		return NetworkRow{}, err
	}

	phase := "Ready"
	var reason, message *string
	if err := a.CheckCollision(ctx, clusterCIDR); err != nil {
		phase = "Failed"
		r := "CidrCollision"
		m := err.Error()
		reason, message = &r, &m
	}

	id := newID("net")
	row := NetworkRow{
		ID:               id,
		Name:             name,
		ClusterCIDR:      clusterCIDR,
		NodePrefixLength: nodePrefix,
		IPv6CIDR:         ipv6,
		Generation:       1,
		ResourceVersion:  1,
		Phase:            phase,
		ConditionReason:  reason,
		ConditionMessage: message,
		CreatedAt:        time.Now().UTC(),
	}

	_, err := a.Pool.Exec(ctx, `
INSERT INTO network.networks (
  id, name, cluster_cidr, node_prefix_length, ipv6_cidr,
  generation, resource_version, phase, condition_reason, condition_message
) VALUES ($1,$2,$3::cidr,$4,$5::cidr,$6,$7,$8,$9,$10)`,
		row.ID, row.Name, row.ClusterCIDR, row.NodePrefixLength, nullCIDR(ipv6),
		row.Generation, row.ResourceVersion, row.Phase, reason, message,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return NetworkRow{}, fmt.Errorf("network name already exists")
		}
		return NetworkRow{}, err
	}
	if phase == "Failed" {
		return row, fmt.Errorf("%w: %s", ErrCidrCollision, *message)
	}
	return row, nil
}

// GetNetworkByName loads a Network by name.
func (a *Allocator) GetNetworkByName(ctx context.Context, name string) (NetworkRow, error) {
	var row NetworkRow
	var ipv6 *string
	var reason, message *string
	err := a.Pool.QueryRow(ctx, `
SELECT id, name, host(network(cluster_cidr)) || '/' || masklen(cluster_cidr),
       node_prefix_length,
       CASE WHEN ipv6_cidr IS NULL THEN NULL ELSE host(network(ipv6_cidr)) || '/' || masklen(ipv6_cidr) END,
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

// ListNetworks returns all networks.
func (a *Allocator) ListNetworks(ctx context.Context) ([]NetworkRow, error) {
	rows, err := a.Pool.Query(ctx, `
SELECT id, name, host(network(cluster_cidr)) || '/' || masklen(cluster_cidr),
       node_prefix_length,
       CASE WHEN ipv6_cidr IS NULL THEN NULL ELSE host(network(ipv6_cidr)) || '/' || masklen(ipv6_cidr) END,
       generation, resource_version, phase, condition_reason, condition_message, created_at
FROM network.networks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NetworkRow
	for rows.Next() {
		var row NetworkRow
		var ipv6 *string
		var reason, message *string
		if err := rows.Scan(
			&row.ID, &row.Name, &row.ClusterCIDR, &row.NodePrefixLength, &ipv6,
			&row.Generation, &row.ResourceVersion, &row.Phase, &reason, &message, &row.CreatedAt,
		); err != nil {
			return nil, err
		}
		row.IPv6CIDR = ipv6
		row.ConditionReason = reason
		row.ConditionMessage = message
		out = append(out, row)
	}
	return out, rows.Err()
}

// MarkFailedNetworks re-checks existing networks against collision sources at startup.
func (a *Allocator) MarkFailedNetworks(ctx context.Context) error {
	nets, err := a.ListNetworks(ctx)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if err := a.CheckCollision(ctx, n.ClusterCIDR); err != nil {
			msg := err.Error()
			reason := "CidrCollision"
			_, _ = a.Pool.Exec(ctx, `
UPDATE network.networks
SET phase = 'Failed', condition_reason = $2, condition_message = $3,
    resource_version = resource_version + 1
WHERE id = $1`, n.ID, reason, msg)
			if a.Log != nil {
				a.Log.Error("network cidr collision",
					"network", n.Name, "cidr", n.ClusterCIDR, "reason", msg)
			}
		}
	}
	return nil
}

// AllocateNodeLease assigns the next free node block (idempotent per node_id).
// Index 0 is reserved; allocation starts at 1.
func (a *Allocator) AllocateNodeLease(ctx context.Context, networkName, nodeID string) (NodeLease, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return NodeLease{}, fmt.Errorf("node_id is required")
	}
	netRow, err := a.GetNetworkByName(ctx, networkName)
	if err != nil {
		return NodeLease{}, err
	}
	if netRow.Phase != "Ready" {
		return NodeLease{}, fmt.Errorf("%w: phase=%s", ErrNetworkNotReady, netRow.Phase)
	}

	// Idempotent: return existing active lease.
	var existingCIDR, existingGW string
	err = a.Pool.QueryRow(ctx, `
SELECT host(network(cidr)) || '/' || masklen(cidr), host(gateway)
FROM network.node_leases
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`,
		netRow.ID, nodeID).Scan(&existingCIDR, &existingGW)
	if err == nil {
		a.logLease(networkName, nodeID, "", existingCIDR, "allocate_idempotent")
		return NodeLease{NodeID: nodeID, CIDR: existingCIDR, Gateway: existingGW}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NodeLease{}, err
	}

	plan, err := ParsePlan(netRow.ClusterCIDR, netRow.NodePrefixLength)
	if err != nil {
		return NodeLease{}, err
	}

	active, err := a.activeNodeCIDRs(ctx, netRow.ID)
	if err != nil {
		return NodeLease{}, err
	}
	used := map[string]struct{}{}
	for _, c := range active {
		used[c] = struct{}{}
	}

	// Prefer reusing a previously released row for this node_id (same PK).
	for idx := 1; idx < plan.NodeBlockCount(); idx++ {
		block, err := plan.NodeBlock(idx)
		if err != nil {
			return NodeLease{}, err
		}
		cidr := block.String()
		if _, taken := used[cidr]; taken {
			continue
		}
		gw, err := GatewayForBlock(block)
		if err != nil {
			return NodeLease{}, err
		}
		tag, err := a.Pool.Exec(ctx, `
INSERT INTO network.node_leases (network_id, node_id, cidr, gateway)
VALUES ($1, $2, $3::cidr, $4::inet)
ON CONFLICT (network_id, node_id) DO UPDATE
SET cidr = EXCLUDED.cidr,
    gateway = EXCLUDED.gateway,
    leased_at = now(),
    released_at = NULL
WHERE network.node_leases.released_at IS NOT NULL`,
			netRow.ID, nodeID, cidr, gw.String())
		if err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return NodeLease{}, err
		}
		if tag.RowsAffected() == 0 {
			// Concurrent idempotent win — re-read.
			err = a.Pool.QueryRow(ctx, `
SELECT host(network(cidr)) || '/' || masklen(cidr), host(gateway)
FROM network.node_leases
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`,
				netRow.ID, nodeID).Scan(&existingCIDR, &existingGW)
			if err == nil {
				return NodeLease{NodeID: nodeID, CIDR: existingCIDR, Gateway: existingGW}, nil
			}
			continue
		}
		a.logLease(networkName, nodeID, "", cidr, "allocate")
		return NodeLease{NodeID: nodeID, CIDR: cidr, Gateway: gw.String()}, nil
	}
	return NodeLease{}, ErrNoAddressSpace
}

// ReleaseNodeLease marks a node block free and orphans its workload leases.
func (a *Allocator) ReleaseNodeLease(ctx context.Context, networkName, nodeID string) error {
	netRow, err := a.GetNetworkByName(ctx, networkName)
	if err != nil {
		return err
	}
	tag, err := a.Pool.Exec(ctx, `
UPDATE network.node_leases
SET released_at = now()
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`,
		netRow.ID, nodeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNodeLeaseNotFound
	}
	_, _ = a.Pool.Exec(ctx, `
UPDATE network.workload_leases
SET released_at = now()
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`,
		netRow.ID, nodeID)
	a.logLease(networkName, nodeID, "", "", "release")
	return nil
}

// AllocateWorkloadLease assigns the next free address in the node's block.
func (a *Allocator) AllocateWorkloadLease(ctx context.Context, networkName, nodeID, workloadID string) (WorkloadLease, error) {
	nodeID = strings.TrimSpace(nodeID)
	workloadID = strings.TrimSpace(workloadID)
	if nodeID == "" || workloadID == "" {
		return WorkloadLease{}, fmt.Errorf("node_id and workload_id are required")
	}
	netRow, err := a.GetNetworkByName(ctx, networkName)
	if err != nil {
		return WorkloadLease{}, err
	}
	if netRow.Phase != "Ready" {
		return WorkloadLease{}, fmt.Errorf("%w: phase=%s", ErrNetworkNotReady, netRow.Phase)
	}

	var existingAddr string
	err = a.Pool.QueryRow(ctx, `
SELECT host(address) FROM network.workload_leases
WHERE network_id = $1 AND workload_id = $2 AND released_at IS NULL`,
		netRow.ID, workloadID).Scan(&existingAddr)
	if err == nil {
		a.logLease(networkName, nodeID, workloadID, existingAddr, "allocate_idempotent")
		return WorkloadLease{WorkloadID: workloadID, NodeID: nodeID, Address: existingAddr}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WorkloadLease{}, err
	}

	var nodeCIDR string
	err = a.Pool.QueryRow(ctx, `
SELECT host(network(cidr)) || '/' || masklen(cidr)
FROM network.node_leases
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`,
		netRow.ID, nodeID).Scan(&nodeCIDR)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkloadLease{}, ErrNodeLeaseNotFound
	}
	if err != nil {
		return WorkloadLease{}, err
	}

	active, err := a.activeWorkloadAddrs(ctx, netRow.ID, nodeID)
	if err != nil {
		return WorkloadLease{}, err
	}
	used := map[string]struct{}{}
	for _, addr := range active {
		used[addr] = struct{}{}
	}

	pfx, err := netip.ParsePrefix(nodeCIDR)
	if err != nil {
		return WorkloadLease{}, fmt.Errorf("node lease cidr: %w", err)
	}
	pfx = pfx.Masked()
	maxOff := MaxWorkloadOffset(pfx)
	for off := FirstWorkloadOffset; off <= maxOff; off++ {
		addr, err := WorkloadAddress(pfx, off)
		if err != nil {
			return WorkloadLease{}, err
		}
		s := addr.String()
		if _, taken := used[s]; taken {
			continue
		}
		tag, err := a.Pool.Exec(ctx, `
INSERT INTO network.workload_leases (network_id, node_id, workload_id, address)
VALUES ($1, $2, $3, $4::inet)
ON CONFLICT (network_id, workload_id) DO UPDATE
SET node_id = EXCLUDED.node_id,
    address = EXCLUDED.address,
    leased_at = now(),
    released_at = NULL
WHERE network.workload_leases.released_at IS NOT NULL`,
			netRow.ID, nodeID, workloadID, s)
		if err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return WorkloadLease{}, err
		}
		if tag.RowsAffected() == 0 {
			err = a.Pool.QueryRow(ctx, `
SELECT host(address) FROM network.workload_leases
WHERE network_id = $1 AND workload_id = $2 AND released_at IS NULL`,
				netRow.ID, workloadID).Scan(&existingAddr)
			if err == nil {
				return WorkloadLease{WorkloadID: workloadID, NodeID: nodeID, Address: existingAddr}, nil
			}
			continue
		}
		a.logLease(networkName, nodeID, workloadID, s, "allocate")
		return WorkloadLease{WorkloadID: workloadID, NodeID: nodeID, Address: s}, nil
	}
	return WorkloadLease{}, ErrNodeBlockExhausted
}

// ActiveWorkloadLease is a listed active address assignment.
type ActiveWorkloadLease struct {
	WorkloadID string `json:"workload_id"`
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
}

// ListActiveWorkloadLeases returns every unreleased workload lease for a network.
func (a *Allocator) ListActiveWorkloadLeases(ctx context.Context, networkName string) ([]ActiveWorkloadLease, error) {
	netRow, err := a.GetNetworkByName(ctx, networkName)
	if err != nil {
		return nil, err
	}
	rows, err := a.Pool.Query(ctx, `
SELECT workload_id, node_id, host(address)
FROM network.workload_leases
WHERE network_id = $1 AND released_at IS NULL
ORDER BY node_id, workload_id`, netRow.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveWorkloadLease
	for rows.Next() {
		var l ActiveWorkloadLease
		if err := rows.Scan(&l.WorkloadID, &l.NodeID, &l.Address); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// HasActiveWorkloadAddress reports whether address is currently leased.
func (a *Allocator) HasActiveWorkloadAddress(ctx context.Context, networkName, address string) (bool, error) {
	netRow, err := a.GetNetworkByName(ctx, networkName)
	if err != nil {
		return false, err
	}
	var n int
	err = a.Pool.QueryRow(ctx, `
SELECT COUNT(*) FROM network.workload_leases
WHERE network_id = $1 AND released_at IS NULL AND host(address) = $2`,
		netRow.ID, strings.TrimSpace(address)).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReleaseWorkloadLease frees a workload address.
func (a *Allocator) ReleaseWorkloadLease(ctx context.Context, networkName, workloadID string) error {
	netRow, err := a.GetNetworkByName(ctx, networkName)
	if err != nil {
		return err
	}
	tag, err := a.Pool.Exec(ctx, `
UPDATE network.workload_leases
SET released_at = now()
WHERE network_id = $1 AND workload_id = $2 AND released_at IS NULL`,
		netRow.ID, workloadID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkloadLeaseMissing
	}
	a.logLease(networkName, "", workloadID, "", "release")
	return nil
}

// ReclaimOrphans releases workload leases whose node lease is no longer active.
func (a *Allocator) ReclaimOrphans(ctx context.Context) (int64, error) {
	tag, err := a.Pool.Exec(ctx, `
UPDATE network.workload_leases wl
SET released_at = now()
WHERE wl.released_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM network.node_leases nl
    WHERE nl.network_id = wl.network_id
      AND nl.node_id = wl.node_id
      AND nl.released_at IS NULL
  )`)
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if n > 0 && a.Log != nil {
		a.Log.Info("reclaimed orphan workload leases", "count", n, "action", "reclaim")
	}
	return n, nil
}

func (a *Allocator) activeNodeCIDRs(ctx context.Context, networkID string) ([]string, error) {
	rows, err := a.Pool.Query(ctx, `
SELECT host(network(cidr)) || '/' || masklen(cidr)
FROM network.node_leases WHERE network_id = $1 AND released_at IS NULL`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *Allocator) activeWorkloadAddrs(ctx context.Context, networkID, nodeID string) ([]string, error) {
	rows, err := a.Pool.Query(ctx, `
SELECT host(address) FROM network.workload_leases
WHERE network_id = $1 AND node_id = $2 AND released_at IS NULL`, networkID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *Allocator) logLease(network, nodeID, workloadID, value, action string) {
	if a.Log == nil {
		return
	}
	a.Log.Info("lease",
		"network", network,
		"node_id", nodeID,
		"workload_id", workloadID,
		"cidr_or_address", value,
		"action", action,
	)
}

func nullCIDR(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return *v
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func newID(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
