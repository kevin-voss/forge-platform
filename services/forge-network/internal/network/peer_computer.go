package network

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

// PeerSetComputer builds full-mesh peer sets and bumps versions for affected nodes.
// Transport selection (22.04) is applied by Runtime: it asks forge-network for the
// per-pair transport and only configures WireGuard for wireguard pairs.
type PeerSetComputer struct {
	Registry   *PeerRegistry
	Membership *MembershipStore
	Log        *slog.Logger
}

// ComputeForNode returns the peer list for nodeID (excluding offline peers and self).
// During rotation, both old and new keys are advertised so traffic never drops.
func (c *PeerSetComputer) ComputeForNode(ctx context.Context, networkName, nodeID string) (PeerSetResponse, error) {
	if c.Registry == nil {
		return PeerSetResponse{}, fmt.Errorf("peer registry not configured")
	}
	netRow, err := c.Registry.networkByName(ctx, networkName)
	if err != nil {
		return PeerSetResponse{}, err
	}
	self, err := c.Registry.getPeer(ctx, netRow.ID, nodeID)
	if err != nil {
		return PeerSetResponse{}, err
	}
	online, err := c.Registry.ListOnline(ctx, netRow.ID)
	if err != nil {
		return PeerSetResponse{}, err
	}

	ka := c.Registry.keepalive()
	peers := make([]PeerEntry, 0, len(online))
	for _, p := range online {
		if p.NodeID == nodeID {
			continue
		}
		if p.CIDR == "" {
			continue
		}
		peers = append(peers, peerEntriesFor(p, ka)...)
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].NodeID == peers[j].NodeID {
			return peers[i].PublicKey < peers[j].PublicKey
		}
		return peers[i].NodeID < peers[j].NodeID
	})

	return PeerSetResponse{
		NodeID:      nodeID,
		PeerVersion: self.PeerSetVersion,
		MTU:         c.Registry.mtu(),
		Peers:       peers,
	}, nil
}

func peerEntriesFor(p PeerRow, keepalive int) []PeerEntry {
	base := PeerEntry{
		NodeID:              p.NodeID,
		PublicKey:           p.PublicKey,
		Endpoint:            p.Endpoint,
		AllowedIPs:          []string{p.CIDR},
		PersistentKeepalive: keepalive,
	}
	out := []PeerEntry{base}
	if (p.Status == PeerStatusRotating || p.Status == PeerStatusRetiring) &&
		p.RotatesTo != nil && *p.RotatesTo != "" && *p.RotatesTo != p.PublicKey {
		out = append(out, PeerEntry{
			NodeID:              p.NodeID,
			PublicKey:           *p.RotatesTo,
			Endpoint:            p.Endpoint,
			AllowedIPs:          []string{p.CIDR},
			PersistentKeepalive: keepalive,
		})
	}
	return out
}

// OnJoin registers (if needed already done) and bumps peer_set_version for every
// online node whose peer set changes (full mesh: all online nodes including joiner).
func (c *PeerSetComputer) OnJoin(ctx context.Context, networkName, nodeID string) (int64, error) {
	return c.recomputeAffected(ctx, networkName, nil)
}

// OnLeave marks the node offline then bumps versions for remaining online nodes.
func (c *PeerSetComputer) OnLeave(ctx context.Context, networkName, nodeID string) (int64, error) {
	if err := c.Registry.Leave(ctx, networkName, nodeID); err != nil {
		return 0, err
	}
	return c.recomputeAffected(ctx, networkName, nil)
}

// OnRotate bumps peer sets for all online nodes (dual-key change is visible to all).
func (c *PeerSetComputer) OnRotate(ctx context.Context, networkName, nodeID string) (int64, error) {
	return c.recomputeAffected(ctx, networkName, nil)
}

// OnRetire bumps peer sets after an old key is removed.
func (c *PeerSetComputer) OnRetire(ctx context.Context, networkName, nodeID string) (int64, error) {
	return c.recomputeAffected(ctx, networkName, nil)
}

// recomputeAffected bumps peer_set_version only for online nodes (incremental:
// offline nodes are not touched). In full mesh every online node is affected.
func (c *PeerSetComputer) recomputeAffected(ctx context.Context, networkName string, only []string) (int64, error) {
	netRow, err := c.Registry.networkByName(ctx, networkName)
	if err != nil {
		return 0, err
	}
	online, err := c.Registry.ListOnline(ctx, netRow.ID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(online))
	allow := map[string]struct{}{}
	for _, id := range only {
		allow[id] = struct{}{}
	}
	for _, p := range online {
		if len(allow) > 0 {
			if _, ok := allow[p.NodeID]; !ok {
				continue
			}
		}
		ids = append(ids, p.NodeID)
	}
	if c.Log != nil {
		c.Log.Info("peer set recompute",
			"event", "network.peers.compute",
			"network", networkName,
			"affected", len(ids),
			"span", "network.peers.compute",
		)
	}
	return c.Registry.BumpPeerSetVersions(ctx, netRow.ID, ids)
}

// OfflineNodeExcluded is a pure helper for unit tests: given online node ids and
// an offline id, each online node should peer with every other online node.
func OfflineNodeExcluded(online []string, offline string) map[string][]string {
	set := make(map[string][]string, len(online))
	for _, a := range online {
		if a == offline {
			continue
		}
		var peers []string
		for _, b := range online {
			if b == a || b == offline {
				continue
			}
			peers = append(peers, b)
		}
		sort.Strings(peers)
		set[a] = peers
	}
	return set
}
