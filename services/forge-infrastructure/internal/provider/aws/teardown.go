package aws

import (
	"context"
	"fmt"
	"strings"
)

// teardownNode deletes a node with ordering: volumes → EIP → instance → (last node) VPC.
func (p *Provider) teardownNode(ctx context.Context, opID string, nodeID string) error {
	instID, region, err := parseNodeID(nodeID)
	if err != nil {
		return err
	}
	if region == "" {
		region = p.cfg.DefaultRegion
	}

	inst, err := p.api.GetInstance(ctx, region, instID)
	pool := ""
	if err == nil && inst != nil && inst.Tags != nil {
		pool = inst.Tags[TagNodePool]
		if inst.Region != "" {
			region = inst.Region
		}
	}

	// 1) Detach + delete exclusively-owned volumes for this instance.
	vols, listErr := p.api.DescribeVolumes(ctx, region, TagFilterManaged())
	if listErr != nil {
		return listErr
	}
	for _, v := range vols {
		if v.InstanceID != instID {
			continue
		}
		if err := p.api.DetachVolume(ctx, region, v.ID); err != nil && !IsNotFound(err) {
			p.log.Warn("aws volume detach failed during teardown",
				"volume_id", v.ID, "error", err.Error(), "op_id", opID)
		}
		p.record("volume.detach")
		if err := p.api.DeleteVolume(ctx, region, v.ID); err != nil && !IsNotFound(err) {
			return fmt.Errorf("delete volume %s: %w", v.ID, err)
		}
		p.record("volume.delete")
	}

	// 2) Release any Elastic IP associated with this instance.
	ips, ipErr := p.api.DescribeAddresses(ctx, region, TagFilterManaged())
	if ipErr != nil {
		return ipErr
	}
	for _, ip := range ips {
		if ip.InstanceID != instID {
			continue
		}
		if ip.AssociationID != "" {
			_ = p.api.DisassociateAddress(ctx, region, ip.AssociationID)
			p.record("eip.disassociate")
		}
		if err := p.api.ReleaseAddress(ctx, region, ip.AllocationID); err != nil && !IsNotFound(err) {
			return fmt.Errorf("release eip %s: %w", ip.AllocationID, err)
		}
		p.record("eip.release")
	}

	// 3) Terminate the instance.
	if err := p.api.TerminateInstance(ctx, region, instID); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("instance.delete")

	// 4) If this was the last managed node in the pool, delete the pool's VPC.
	if pool != "" {
		remaining, err := p.api.DescribeInstances(ctx, region, TagFilterPool(pool))
		if err != nil {
			return err
		}
		stillThere := 0
		for _, s := range remaining {
			if s.ID == instID {
				continue
			}
			if strings.EqualFold(s.State, "terminated") {
				continue
			}
			stillThere++
		}
		if stillThere == 0 {
			nets, err := p.api.DescribeVPCs(ctx, region, TagFilterPool(pool))
			if err != nil {
				return err
			}
			for _, n := range nets {
				if err := p.api.DeleteVPC(ctx, region, n.ID); err != nil && !IsNotFound(err) {
					return fmt.Errorf("delete vpc %s: %w", n.ID, err)
				}
				p.record("vpc.delete")
				p.log.Info("aws pool network deleted (last node)",
					"event", "infra.provider.aws.delete_network",
					"vpc_id", n.ID,
					"node_pool", pool,
					"op_id", opID,
				)
			}
		}
	}

	p.log.Info("aws delete_node ok",
		"event", "infra.provider.aws.delete_node",
		"instance_id", instID,
		"node_pool", pool,
		"action", "delete",
		"op_id", opID,
	)
	return nil
}
