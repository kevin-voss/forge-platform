package azure

import (
	"context"
	"fmt"
	"strings"
)

// teardownNode deletes a node with ordering: disks → public IP → VM → (last node) VNet.
func (p *Provider) teardownNode(ctx context.Context, opID string, nodeID string) error {
	vmID, err := parseNodeID(nodeID)
	if err != nil {
		return err
	}

	vm, err := p.api.GetVM(ctx, vmID)
	pool := ""
	if err == nil && vm != nil && vm.Tags != nil {
		pool = vm.Tags[TagNodePool]
	}

	disks, listErr := p.api.ListDisks(ctx, TagFilterManaged())
	if listErr != nil {
		return listErr
	}
	for _, d := range disks {
		if d.VMID != vmID {
			continue
		}
		if err := p.api.DetachDisk(ctx, d.ID); err != nil && !IsNotFound(err) {
			p.log.Warn("azure disk detach failed during teardown",
				"disk_id", d.ID, "error", err.Error(), "op_id", opID)
		}
		p.record("disk.detach")
		if err := p.api.DeleteDisk(ctx, d.ID); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete disk %s: %w", d.ID, err)
		}
		p.record("disk.delete")
	}

	ips, ipErr := p.api.ListPublicIPs(ctx, TagFilterManaged())
	if ipErr != nil {
		return ipErr
	}
	for _, ip := range ips {
		if ip.VMID != vmID {
			continue
		}
		_ = p.api.DisassociatePublicIP(ctx, ip.ID)
		p.record("pip.disassociate")
		if err := p.api.DeletePublicIP(ctx, ip.ID); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete public ip %s: %w", ip.ID, err)
		}
		p.record("pip.delete")
	}

	if err := p.api.DeleteVM(ctx, vmID); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("vm.delete")

	if pool != "" {
		remaining, err := p.api.ListVMs(ctx, TagFilterPool(pool))
		if err != nil {
			return err
		}
		stillThere := 0
		for _, s := range remaining {
			if s.ID == vmID {
				continue
			}
			if strings.EqualFold(s.PowerState, "deleting") {
				continue
			}
			stillThere++
		}
		if stillThere == 0 {
			nets, err := p.api.ListVNets(ctx, TagFilterPool(pool))
			if err != nil {
				return err
			}
			for _, n := range nets {
				if err := p.api.DeleteVNet(ctx, n.ID); err != nil && !IsNotFound(err) {
					return fmt.Errorf("delete vnet %s: %w", n.ID, err)
				}
				p.record("vnet.delete")
				p.log.Info("azure pool network deleted (last node)",
					"event", "infra.provider.azure.delete_network",
					"vnet_id", n.ID, "node_pool", pool, "op_id", opID,
				)
			}
		}
	}

	p.log.Info("azure delete_node ok",
		"event", "infra.provider.azure.delete_node",
		"vm_id", vmID, "node_pool", pool, "action", "delete", "op_id", opID,
	)
	return nil
}
