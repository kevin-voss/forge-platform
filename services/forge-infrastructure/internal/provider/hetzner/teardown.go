package hetzner

import (
	"context"
	"fmt"
)

// teardownNode deletes a node with ordering: volumes → floating IP → server → (last node) network.
func (p *Provider) teardownNode(ctx context.Context, opID string, nodeID string) error {
	serverID, err := parseNodeID(nodeID)
	if err != nil {
		return err
	}

	srv, err := p.api.GetServer(ctx, serverID)
	pool := ""
	if err == nil && srv != nil && srv.Labels != nil {
		pool = srv.Labels[LabelNodePool]
	}
	// Proceed even if GetServer fails (already gone) — still try cleanup by labels.

	// 1) Detach + delete exclusively-owned volumes for this server.
	vols, listErr := p.api.ListVolumes(ctx, LabelSelectorManaged())
	if listErr != nil {
		return listErr
	}
	for _, v := range vols {
		if v.Server == nil || *v.Server != serverID {
			continue
		}
		if err := p.api.DetachVolume(ctx, v.ID); err != nil && !IsNotFound(err) {
			p.log.Warn("hetzner volume detach failed during teardown",
				"volume_id", v.ID, "error", err.Error(), "op_id", opID)
		}
		p.record("volume.detach")
		if err := p.api.DeleteVolume(ctx, v.ID); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete volume %d: %w", v.ID, err)
		}
		p.record("volume.delete")
	}

	// 2) Release any assigned Floating IP for this server.
	ips, ipErr := p.api.ListFloatingIPs(ctx, LabelSelectorManaged())
	if ipErr != nil {
		return ipErr
	}
	for _, ip := range ips {
		if ip.Server == nil || *ip.Server != serverID {
			continue
		}
		_ = p.api.UnassignFloatingIP(ctx, ip.ID)
		p.record("floating_ip.unassign")
		if err := p.api.DeleteFloatingIP(ctx, ip.ID); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete floating ip %d: %w", ip.ID, err)
		}
		p.record("floating_ip.delete")
	}

	// 3) Delete the server.
	if err := p.api.DeleteServer(ctx, serverID); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("server.delete")

	// 4) If this was the last managed node in the pool, delete the pool's private network.
	if pool != "" {
		remaining, err := p.api.ListServers(ctx, LabelSelectorPool(pool))
		if err != nil {
			return err
		}
		stillThere := 0
		for _, s := range remaining {
			if s.ID == serverID {
				continue
			}
			stillThere++
		}
		if stillThere == 0 {
			nets, err := p.api.ListNetworks(ctx, LabelSelectorPool(pool))
			if err != nil {
				return err
			}
			for _, n := range nets {
				if err := p.api.DeleteNetwork(ctx, n.ID); err != nil && !IsNotFound(err) {
					return fmt.Errorf("delete network %d: %w", n.ID, err)
				}
				p.record("network.delete")
				p.log.Info("hetzner pool network deleted (last node)",
					"event", "infra.provider.hetzner.delete_network",
					"network_id", n.ID,
					"node_pool", pool,
					"op_id", opID,
				)
			}
		}
	}

	p.log.Info("hetzner delete_node ok",
		"event", "infra.provider.hetzner.delete_node",
		"server_id", serverID,
		"node_pool", pool,
		"action", "delete",
		"op_id", opID,
	)
	return nil
}
