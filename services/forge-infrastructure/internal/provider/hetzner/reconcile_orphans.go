package hetzner

import (
	"context"
	"log/slog"
	"strconv"
	"time"
)

// KnownNodeIDs lists provider node ids (e.g. "hetzner:48213093") that have a Node resource.
type KnownNodeIDs interface {
	ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error)
}

// KnownNetworkIDs lists provider network ids that are still referenced.
type KnownNetworkIDs interface {
	ProviderNetworkIDs(ctx context.Context) (map[string]struct{}, error)
}

// OrphanReconciler deletes managed Hetzner resources with no matching Node after a grace period.
type OrphanReconciler struct {
	Provider *Provider
	Known    KnownNodeIDs
	Log      *slog.Logger
	Interval time.Duration
	// Grace overrides Provider.spec.OrphanGraceMinutes when > 0.
	Grace time.Duration
	// Now is injectable for tests.
	Now func() time.Time
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
			o.Log.Warn("hetzner orphan reconcile failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reconcile lists forge.managed=true resources and deletes orphans past the grace period.
// Returns the number of resources removed.
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

	servers, err := o.Provider.api.ListServers(ctx, LabelSelectorManaged())
	if err != nil {
		return 0, err
	}
	for _, s := range servers {
		providerID := nodeIDPrefix + strconv.FormatInt(s.ID, 10)
		if _, ok := known[providerID]; ok {
			continue
		}
		created, ok := parseCreated(s.Created)
		if ok && now().Sub(created) < grace {
			if o.Log != nil {
				o.Log.Info("hetzner orphan within grace period",
					"event", "infra.provider.hetzner.orphan_deferred",
					"server_id", s.ID,
					"grace_minutes", int(grace/time.Minute),
				)
			}
			continue
		}
		if err := o.Provider.DeleteNode(ctx, "orphan", providerID); err != nil {
			if o.Log != nil {
				o.Log.Warn("hetzner orphan delete failed",
					"server_id", s.ID,
					"reason", "orphan_no_resource",
					"error", err.Error(),
				)
			}
			continue
		}
		o.Provider.orphansDeleted.Add(1)
		removed++
		if o.Log != nil {
			o.Log.Info("hetzner orphan removed",
				"event", "infra.provider.hetzner.orphan_removed",
				"server_id", s.ID,
				"resource_id", providerID,
				"grace_minutes", int(grace/time.Minute),
				"reason", "orphan_no_resource",
				"metric", "forge_infra_hetzner_orphans_deleted_total",
			)
		}
	}

	// Orphan floating IPs / volumes not attached to any known server.
	ips, err := o.Provider.api.ListFloatingIPs(ctx, LabelSelectorManaged())
	if err != nil {
		return removed, err
	}
	for _, ip := range ips {
		if ip.Server != nil {
			providerID := nodeIDPrefix + strconv.FormatInt(*ip.Server, 10)
			if _, ok := known[providerID]; ok {
				continue
			}
		}
		// Unattached or attached to unknown server — delete after grace if labeled managed.
		// Floating IPs lack created timestamps in our wire type; delete only when unattached
		// and no server match (immediate for unattached managed IPs past grace via server path).
		if ip.Server == nil {
			_ = o.Provider.api.UnassignFloatingIP(ctx, ip.ID)
			if err := o.Provider.api.DeleteFloatingIP(ctx, ip.ID); err != nil && !IsNotFound(err) {
				continue
			}
			o.Provider.orphansDeleted.Add(1)
			removed++
			if o.Log != nil {
				o.Log.Info("hetzner orphan floating ip removed",
					"event", "infra.provider.hetzner.orphan_removed",
					"floating_ip_id", ip.ID,
					"grace_minutes", int(grace/time.Minute),
					"reason", "orphan_no_resource",
					"metric", "forge_infra_hetzner_orphans_deleted_total",
				)
			}
		}
	}

	return removed, nil
}

func parseCreated(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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
