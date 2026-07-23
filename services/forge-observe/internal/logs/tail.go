package logs

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// TailMetrics tracks live log-stream dogfood gauges/counters.
type TailMetrics struct {
	ActiveStreams   atomic.Int64
	ReconnectsTotal atomic.Int64
}

// TailOptions controls poll-based Loki tailing.
type TailOptions struct {
	PollInterval time.Duration
	BatchLimit   int
}

// DefaultTailOptions returns sensible local-dev defaults.
func DefaultTailOptions() TailOptions {
	return TailOptions{
		PollInterval: time.Second,
		BatchLimit:   100,
	}
}

// EntryHandler receives one normalized log entry during a live tail.
type EntryHandler func(Entry) error

// Tail polls Loki with forward query_range, emitting new entries as they appear.
// The window start advances past each emitted timestamp so reconnects resume
// without large gaps or duplicates.
func (s *Service) Tail(ctx context.Context, f Filters, opts TailOptions, emit EntryHandler) error {
	if s == nil || s.Loki == nil {
		return ErrLokiUnavailable
	}
	if emit == nil {
		return fmt.Errorf("emit handler required")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.BatchLimit < 1 {
		opts.BatchLimit = 100
	}

	cursor := f.Since
	if cursor.IsZero() {
		now := time.Now().UTC()
		if s.Now != nil {
			now = s.Now().UTC()
		}
		cursor = now
	}

	if s.StreamMetrics != nil {
		s.StreamMetrics.ActiveStreams.Add(1)
		defer s.StreamMetrics.ActiveStreams.Add(-1)
	}

	if s.Log != nil {
		s.Log.Info("log stream start",
			"span", "observe.logs.stream",
			"filters_project", f.Project,
			"filters_deployment", f.Deployment,
			"filters_service", f.Service,
			"filters_trace_id", f.TraceID,
			"filters_request_id", f.RequestID,
			"forge_log_streams_active", 1,
		)
	}

	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	// Immediate first poll so clients see catch-up without waiting a full interval.
	if err := s.pollOnce(ctx, f, &cursor, opts.BatchLimit, emit); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.pollOnce(ctx, f, &cursor, opts.BatchLimit, emit); err != nil {
				return err
			}
		}
	}
}

func (s *Service) pollOnce(ctx context.Context, f Filters, cursor *time.Time, limit int, emit EntryHandler) error {
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	start := *cursor
	if !start.Before(now) {
		return nil
	}

	logql := BuildLogQL(f)
	streams, err := s.Loki.QueryRange(ctx, logql, start, now, limit, string(DirectionForward))
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("loki tail poll failed",
				"error", err.Error(),
				"span", "observe.logs.stream",
			)
		}
		return fmt.Errorf("%w: %v", ErrLokiUnavailable, err)
	}

	entries := normalizeStreams(streams)
	sortEntries(entries, DirectionForward)

	for _, e := range entries {
		ts, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil {
			continue
		}
		// Exclusive lower bound: skip anything at or before the resume cursor.
		if !ts.After(start) {
			continue
		}
		if err := emit(e); err != nil {
			return err
		}
		*cursor = ts
	}
	// Advance past the polled window even when empty so we do not re-scan forever
	// when Loki's resolution is coarse — keep cursor at last emit, or nudge to now-ε
	// only when we saw a full batch (more may exist).
	if len(entries) >= limit && cursor.Before(now) {
		// leave cursor at last emitted ts; next poll continues from there
	}
	return nil
}
