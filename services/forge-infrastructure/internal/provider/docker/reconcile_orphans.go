package docker

import (
	"context"
	"log/slog"
	"time"
)

// KnownNodeIDs lists provider node ids (e.g. "docker:<containerId>") that have a Node resource.
type KnownNodeIDs interface {
	ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error)
}

// OrphanReconciler deletes managed containers that have no matching Node resource.
type OrphanReconciler struct {
	Provider *Provider
	Known    KnownNodeIDs
	Log      *slog.Logger
	Interval time.Duration
}

// Run polls until ctx is cancelled.
func (o *OrphanReconciler) Run(ctx context.Context) {
	interval := o.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := o.Reconcile(ctx); err != nil && o.Log != nil {
			o.Log.Warn("orphan reconcile failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reconcile lists forge.managed containers and deletes those without a Node resource.
// Returns the number of containers removed.
func (o *OrphanReconciler) Reconcile(ctx context.Context) (int, error) {
	if o.Provider == nil || o.Known == nil {
		return 0, nil
	}
	known, err := o.Known.ProviderNodeIDs(ctx)
	if err != nil {
		return 0, err
	}
	list, err := o.Provider.engine.ContainerList(ctx, map[string][]string{
		"label": {LabelManaged + "=" + LabelManagedValue},
	}, true)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, c := range list {
		providerID := nodeIDPrefix + c.ID
		if _, ok := known[providerID]; ok {
			continue
		}
		// Also accept short-id form if callers store truncated ids.
		short := nodeIDPrefix + shortContainerID(c.ID)
		if _, ok := known[short]; ok {
			continue
		}
		pool := ""
		if c.Labels != nil {
			pool = c.Labels[LabelPool]
		}
		if err := o.Provider.DeleteNode(ctx, "orphan", providerID); err != nil {
			if o.Log != nil {
				o.Log.Warn("orphan delete failed",
					"container_id", shortContainerID(c.ID),
					"node_pool", pool,
					"reason", "orphan_no_resource",
					"error", err.Error(),
				)
			}
			continue
		}
		o.Provider.orphansRemoved.Add(1)
		removed++
		if o.Log != nil {
			o.Log.Info("orphan container removed",
				"event", "infra.provider.docker.orphan_removed",
				"container_id", shortContainerID(c.ID),
				"node_pool", pool,
				"reason", "orphan_no_resource",
				"metric", "forge_infra_docker_orphans_removed_total",
			)
		}
	}
	return removed, nil
}
