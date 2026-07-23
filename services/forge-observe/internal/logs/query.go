package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"forge.local/services/forge-observe/internal/correlation"
)

// ErrLokiUnavailable is returned when Loki cannot be queried.
var ErrLokiUnavailable = errors.New("loki unavailable")

// Entry is one normalized log line in the correlation field set.
type Entry struct {
	Time       string `json:"time"`
	Service    string `json:"service"`
	TraceID    string `json:"trace_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Level      string `json:"level,omitempty"`
	Message    string `json:"message"`
	Deployment string `json:"deployment,omitempty"`
	Project    string `json:"project,omitempty"`
	SpanID     string `json:"span_id,omitempty"`
	Node       string `json:"node,omitempty"`
}

// Result is a paginated log query response.
type Result struct {
	Entries    []Entry  `json:"entries"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Capped     bool     `json:"capped,omitempty"`
}

// StreamValue is one Loki stream value pair.
type StreamValue struct {
	Timestamp time.Time
	Line      string
	Labels    map[string]string
}

// Querier executes a LogQL query_range against Loki.
type Querier interface {
	QueryRange(ctx context.Context, logql string, start, end time.Time, limit int, direction string) ([]StreamValue, error)
}

// Metrics tracks log-query dogfood counters.
type Metrics struct {
	QueriesTotal atomic.Int64
	CappedTotal  atomic.Int64
	LastDuration atomic.Int64 // milliseconds
}

// Service builds LogQL, queries Loki, and normalizes results.
type Service struct {
	Loki    Querier
	Caps    Caps
	Log     *slog.Logger
	Metrics *Metrics
	Now     func() time.Time
}

// Query runs a validated filter set against Loki.
func (s *Service) Query(ctx context.Context, f Filters) (Result, error) {
	if s == nil || s.Loki == nil {
		return Result{}, ErrLokiUnavailable
	}
	start := f.Since
	end := f.Until
	if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err != nil {
			return Result{}, fmt.Errorf("cursor: %w", err)
		}
		if !cur.IsZero() {
			switch f.Direction {
			case DirectionForward:
				start = cur.Add(time.Nanosecond)
			default:
				end = cur.Add(-time.Nanosecond)
			}
		}
	}
	if !start.Before(end) {
		return Result{Entries: []Entry{}, Warnings: f.Warnings, Capped: f.Capped}, nil
	}

	logql := BuildLogQL(f)
	// Fetch one extra to detect a next page.
	fetchLimit := f.Limit + 1
	dir := string(f.Direction)
	if dir == "" {
		dir = string(DirectionBackward)
	}

	began := time.Now()
	streams, err := s.Loki.QueryRange(ctx, logql, start, end, fetchLimit, dir)
	elapsed := time.Since(began)
	if s.Metrics != nil {
		s.Metrics.QueriesTotal.Add(1)
		s.Metrics.LastDuration.Store(elapsed.Milliseconds())
		if f.Capped {
			s.Metrics.CappedTotal.Add(1)
		}
	}
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("loki query failed",
				"error", err.Error(),
				"span", "observe.logs.query",
				"duration_ms", elapsed.Milliseconds(),
			)
		}
		return Result{}, fmt.Errorf("%w: %v", ErrLokiUnavailable, err)
	}

	entries := normalizeStreams(streams)
	sortEntries(entries, f.Direction)

	var next string
	if len(entries) > f.Limit {
		entries = entries[:f.Limit]
		last := entries[len(entries)-1]
		if t, err := time.Parse(time.RFC3339Nano, last.Time); err == nil {
			next = encodeCursor(t)
		}
	}

	if s.Log != nil {
		s.Log.Info("log query",
			"span", "observe.logs.query",
			"filters_project", f.Project,
			"filters_deployment", f.Deployment,
			"filters_service", f.Service,
			"filters_trace_id", f.TraceID,
			"filters_request_id", f.RequestID,
			"filters_q_set", f.Q != "",
			"result_count", len(entries),
			"duration_ms", elapsed.Milliseconds(),
			"capped", f.Capped,
			"forge_log_queries_total", 1,
			"forge_log_query_duration_ms", elapsed.Milliseconds(),
		)
	}

	return Result{
		Entries:    entries,
		NextCursor: next,
		Warnings:   f.Warnings,
		Capped:     f.Capped,
	}, nil
}

func sortEntries(entries []Entry, dir Direction) {
	sort.SliceStable(entries, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, entries[i].Time)
		tj, _ := time.Parse(time.RFC3339Nano, entries[j].Time)
		if dir == DirectionForward {
			if ti.Equal(tj) {
				return entries[i].Service < entries[j].Service
			}
			return ti.Before(tj)
		}
		if ti.Equal(tj) {
			return entries[i].Service < entries[j].Service
		}
		return ti.After(tj)
	})
}

func normalizeStreams(streams []StreamValue) []Entry {
	out := make([]Entry, 0, len(streams))
	for _, sv := range streams {
		out = append(out, normalizeLine(sv))
	}
	return out
}

func normalizeLine(sv StreamValue) Entry {
	e := Entry{
		Time:    sv.Timestamp.UTC().Format(time.RFC3339Nano),
		Message: sv.Line,
	}
	if e.Time == "" || sv.Timestamp.IsZero() {
		e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(sv.Line), &payload); err == nil {
		e.Message = firstString(payload, "message", "msg", "body")
		if e.Message == "" {
			e.Message = sv.Line
		}
		e.Level = firstString(payload, "level", "severity")
		e.TraceID = firstString(payload, correlation.LogTraceID, "traceId")
		e.RequestID = firstString(payload, correlation.LogRequestID, "requestId")
		e.SpanID = firstString(payload, correlation.LogSpanID, "spanId")
		e.Service = firstString(payload, "service", correlation.AttrService, "forge_service")
		e.Deployment = firstString(payload, "deployment", correlation.AttrDeployment, "forge_deployment")
		e.Project = firstString(payload, "project", correlation.AttrProject, "forge_project")
		e.Node = firstString(payload, "node", correlation.AttrNode, "forge_node")
	}

	if e.Service == "" {
		e.Service = firstLabel(sv.Labels, "forge_service", "service_name", "service", "job")
	}
	if e.Deployment == "" {
		e.Deployment = firstLabel(sv.Labels, "forge_deployment")
	}
	if e.Project == "" {
		e.Project = firstLabel(sv.Labels, "forge_project")
	}
	if e.Level == "" {
		e.Level = "info"
	}
	return e
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return t
				}
			case fmt.Stringer:
				s := t.String()
				if strings.TrimSpace(s) != "" {
					return s
				}
			default:
				s := fmt.Sprint(t)
				if strings.TrimSpace(s) != "" && s != "<nil>" {
					return s
				}
			}
		}
	}
	return ""
}

func firstLabel(labels map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(labels[k]); v != "" {
			return v
		}
	}
	return ""
}

// FilterByProjects keeps entries whose project is in allowed (empty project kept only if allowEmpty).
func FilterByProjects(entries []Entry, allowed map[string]struct{}, allowEmpty bool) []Entry {
	if allowed == nil {
		return entries
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		p := strings.TrimSpace(e.Project)
		if p == "" {
			if allowEmpty {
				out = append(out, e)
			}
			continue
		}
		if _, ok := allowed[p]; ok {
			out = append(out, e)
		}
	}
	return out
}
