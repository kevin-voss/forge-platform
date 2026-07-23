package aws

import (
	"context"
	"log/slog"
	"time"
)

// KnownNodeIDs lists provider node ids (e.g. "aws:i-0abc") that have a Node resource.
type KnownNodeIDs interface {
	ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error)
}

// OrphanReconciler deletes managed AWS resources with no matching Node after a grace period.
type OrphanReconciler struct {
	Provider *Provider
	Known    KnownNodeIDs
	Log      *slog.Logger
	Interval time.Duration
	Grace    time.Duration
	Now      func() time.Time
}

// Run polls until ctx is cancelled.
func (o *OrphanReconciler) Run(ctx context.Context) {
	interval := o.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := o.Reconcile(ctx); err != nil && o.Log != nil {
			o.Log.Warn("aws orphan reconcile failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reconcile lists forge.managed=true instances and deletes orphans past the grace period.
func (o *OrphanReconciler) Reconcile(ctx context.Context) (int, error) {
	if o.Provider == nil || o.Known == nil {
		return 0, nil
	}
	known, err := o.Known.ProviderNodeIDs(ctx)
	if err != nil {
		return 0, err
	}
	grace := o.Grace
	if grace <= 0 {
		mins := o.Provider.spec.OrphanGraceMinutes
		if mins < 1 {
			mins = defaultOrphanGraceMinutes
		}
		grace = time.Duration(mins) * time.Minute
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	removed := 0
	region := o.Provider.cfg.DefaultRegion

	insts, err := o.Provider.api.DescribeInstances(ctx, region, TagFilterManaged())
	if err != nil {
		return 0, err
	}
	for _, s := range insts {
		providerID := nodeIDPrefix + s.ID
		if _, ok := known[providerID]; ok {
			continue
		}
		if !s.Created.IsZero() && now().Sub(s.Created) < grace {
			if o.Log != nil {
				o.Log.Info("aws orphan within grace period",
					"event", "infra.provider.aws.orphan_deferred",
					"instance_id", s.ID,
					"grace_minutes", int(grace/time.Minute),
				)
			}
			continue
		}
		if err := o.Provider.DeleteNode(ctx, "orphan", providerID); err != nil {
			if o.Log != nil {
				o.Log.Warn("aws orphan delete failed",
					"instance_id", s.ID,
					"reason", "orphan_no_resource",
					"error", err.Error(),
				)
			}
			continue
		}
		o.Provider.orphansDeleted.Add(1)
		removed++
		if o.Log != nil {
			o.Log.Info("aws orphan removed",
				"event", "infra.provider.aws.orphan_removed",
				"instance_id", s.ID,
				"resource_id", providerID,
				"grace_minutes", int(grace/time.Minute),
				"reason", "orphan_no_resource",
				"metric", "forge_infra_aws_orphans_deleted_total",
			)
		}
	}

	ips, err := o.Provider.api.DescribeAddresses(ctx, region, TagFilterManaged())
	if err != nil {
		return removed, err
	}
	for _, ip := range ips {
		if ip.InstanceID != "" {
			providerID := nodeIDPrefix + ip.InstanceID
			if _, ok := known[providerID]; ok {
				continue
			}
		}
		if ip.InstanceID == "" {
			if ip.AssociationID != "" {
				_ = o.Provider.api.DisassociateAddress(ctx, region, ip.AssociationID)
			}
			if err := o.Provider.api.ReleaseAddress(ctx, region, ip.AllocationID); err != nil && !IsNotFound(err) {
				continue
			}
			o.Provider.orphansDeleted.Add(1)
			removed++
			if o.Log != nil {
				o.Log.Info("aws orphan eip removed",
					"event", "infra.provider.aws.orphan_removed",
					"allocation_id", ip.AllocationID,
					"grace_minutes", int(grace/time.Minute),
					"reason", "orphan_no_resource",
					"metric", "forge_infra_aws_orphans_deleted_total",
				)
			}
		}
	}

	return removed, nil
}

// MapKnown is a static KnownNodeIDs for tests.
type MapKnown map[string]struct{}

func (m MapKnown) ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error) {
	_ = ctx
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out, nil
}
