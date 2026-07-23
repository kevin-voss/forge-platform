package azure

import (
	"context"
	"log/slog"
	"time"
)

// KnownNodeIDs lists provider node ids (e.g. "azure:vm-1") that have a Node resource.
type KnownNodeIDs interface {
	ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error)
}

// OrphanReconciler deletes managed Azure resources with no matching Node after a grace period.
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
			o.Log.Warn("azure orphan reconcile failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reconcile lists forge.managed=true VMs and deletes orphans past the grace period.
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

	vms, err := o.Provider.api.ListVMs(ctx, TagFilterManaged())
	if err != nil {
		return 0, err
	}
	for _, s := range vms {
		providerID := nodeIDPrefix + s.ID
		if _, ok := known[providerID]; ok {
			continue
		}
		if !s.Created.IsZero() && now().Sub(s.Created) < grace {
			if o.Log != nil {
				o.Log.Info("azure orphan within grace period",
					"event", "infra.provider.azure.orphan_deferred",
					"vm_id", s.ID,
					"grace_minutes", int(grace/time.Minute),
				)
			}
			continue
		}
		if err := o.Provider.DeleteNode(ctx, "orphan", providerID); err != nil {
			if o.Log != nil {
				o.Log.Warn("azure orphan delete failed",
					"vm_id", s.ID, "reason", "orphan_no_resource", "error", err.Error(),
				)
			}
			continue
		}
		o.Provider.orphansDeleted.Add(1)
		removed++
		if o.Log != nil {
			o.Log.Info("azure orphan removed",
				"event", "infra.provider.azure.orphan_removed",
				"vm_id", s.ID, "resource_id", providerID,
				"grace_minutes", int(grace/time.Minute),
				"reason", "orphan_no_resource",
				"metric", "forge_infra_azure_orphans_deleted_total",
			)
		}
	}

	ips, err := o.Provider.api.ListPublicIPs(ctx, TagFilterManaged())
	if err != nil {
		return removed, err
	}
	for _, ip := range ips {
		if ip.VMID != "" {
			providerID := nodeIDPrefix + ip.VMID
			if _, ok := known[providerID]; ok {
				continue
			}
		}
		if ip.VMID == "" {
			_ = o.Provider.api.DisassociatePublicIP(ctx, ip.ID)
			if err := o.Provider.api.DeletePublicIP(ctx, ip.ID); err != nil && !IsNotFound(err) {
				continue
			}
			o.Provider.orphansDeleted.Add(1)
			removed++
			if o.Log != nil {
				o.Log.Info("azure orphan public ip removed",
					"event", "infra.provider.azure.orphan_removed",
					"ip_id", ip.ID,
					"grace_minutes", int(grace/time.Minute),
					"reason", "orphan_no_resource",
					"metric", "forge_infra_azure_orphans_deleted_total",
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
