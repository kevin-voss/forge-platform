package sweeper

import (
	"context"
	"log/slog"
	"time"

	"forge.local/services/forge-discovery/internal/store"
)

// Store is the persistence surface used by the lease sweeper.
type Store interface {
	ExpireLeases(ctx context.Context, now time.Time) ([]store.EndpointRow, error)
	ReapUnready(ctx context.Context, cutoff time.Time) ([]store.EndpointRow, error)
}

// MirrorNotifier receives expiry notifications for async Control projection.
type MirrorNotifier interface {
	NotifyEndpointUnready(id, reason string)
}

// WatchPublisher receives watch events for expired/reaped endpoints.
type WatchPublisher interface {
	PublishUpdated(row store.EndpointRow)
	PublishRemoved(row store.EndpointRow)
}

// Metrics records sweeper counters (optional).
type Metrics interface {
	IncLeaseExpirations(n int)
}

// Config controls sweep cadence and reap grace.
type Config struct {
	Interval  time.Duration
	ReapAfter time.Duration
}

// Runner periodically expires leases and reaps long-Unready endpoints.
type Runner struct {
	Store  Store
	Log    *slog.Logger
	Cfg    Config
	Mirror MirrorNotifier
	Watch  WatchPublisher
	Metric Metrics
	Now    func() time.Time
}

// Run blocks until ctx is cancelled. Performs an immediate sweep first.
func (r *Runner) Run(ctx context.Context) {
	if r.Cfg.Interval <= 0 {
		r.Cfg.Interval = 5 * time.Second
	}
	if r.Cfg.ReapAfter <= 0 {
		r.Cfg.ReapAfter = 300 * time.Second
	}
	if r.Now == nil {
		r.Now = time.Now
	}

	r.sweepOnce(ctx)
	ticker := time.NewTicker(r.Cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweepOnce(ctx)
		}
	}
}

// SweepOnce runs a single expire+reap cycle (exported for tests).
func (r *Runner) SweepOnce(ctx context.Context) {
	r.sweepOnce(ctx)
}

func (r *Runner) sweepOnce(ctx context.Context) {
	now := r.Now().UTC()
	rows, err := r.Store.ExpireLeases(ctx, now)
	if err != nil {
		if r.Log != nil {
			r.Log.Error("lease expiry failed", "error", err.Error())
		}
	} else {
		if r.Metric != nil && len(rows) > 0 {
			r.Metric.IncLeaseExpirations(len(rows))
		}
		for _, row := range rows {
			if r.Log != nil {
				r.Log.Info("endpoint unready",
					"event", "discovery.endpoint.unready",
					"id", row.ID,
					"reason", "LeaseExpired",
				)
			}
			if r.Mirror != nil {
				r.Mirror.NotifyEndpointUnready(row.ID, "LeaseExpired")
			}
			if r.Watch != nil {
				r.Watch.PublishUpdated(row)
			}
		}
	}

	cutoff := now.Add(-r.Cfg.ReapAfter)
	reaped, err := r.Store.ReapUnready(ctx, cutoff)
	if err != nil {
		if r.Log != nil {
			r.Log.Error("reap unready failed", "error", err.Error())
		}
		return
	}
	if len(reaped) > 0 && r.Log != nil {
		r.Log.Info("reaped unready endpoints", "count", len(reaped), "reap_after", r.Cfg.ReapAfter.String())
	}
	if r.Watch != nil {
		for _, row := range reaped {
			r.Watch.PublishRemoved(row)
		}
	}
}

// Compile-time check that store.DB satisfies Store.
var _ Store = (*store.DB)(nil)
