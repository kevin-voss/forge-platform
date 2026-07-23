package dlq

import (
	"context"
	"log/slog"
	"time"
)

// RetentionRunner periodically purges DLQ entries older than retention.
type RetentionRunner struct {
	store     *Store
	router    *Router
	retention time.Duration
	interval  time.Duration
	log       *slog.Logger
	metrics   *Metrics
}

// NewRetentionRunner constructs a retention cleaner. interval defaults to 1h.
func NewRetentionRunner(store *Store, router *Router, retentionDays int, log *slog.Logger, metrics *Metrics) *RetentionRunner {
	if log == nil {
		log = slog.Default()
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	if retentionDays < 1 {
		retentionDays = 7
	}
	return &RetentionRunner{
		store:     store,
		router:    router,
		retention: time.Duration(retentionDays) * 24 * time.Hour,
		interval:  time.Hour,
		log:       log,
		metrics:   metrics,
	}
}

// Run blocks until ctx is cancelled, purging expired DLQ entries and flushing
// route retries on each tick.
func (r *RetentionRunner) Run(ctx context.Context) {
	if r == nil {
		return
	}
	t := time.NewTicker(r.interval)
	defer t.Stop()
	r.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *RetentionRunner) tick(ctx context.Context) {
	if r.router != nil {
		r.router.FlushRetries(ctx)
	}
	if r.store == nil {
		return
	}
	cutoff := time.Now().UTC().Add(-r.retention)
	n := r.store.PurgeOlderThan(cutoff)
	if n > 0 {
		r.metrics.Size.Store(int64(r.store.Size()))
		r.log.Info("dlq retention cleanup",
			"span", "events.dlq.retention",
			"removed", n,
			"retention", r.retention.String(),
			"size", r.store.Size(),
		)
	}
}
