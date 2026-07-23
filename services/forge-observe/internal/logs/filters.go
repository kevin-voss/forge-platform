package logs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Direction is Loki query_range direction.
type Direction string

const (
	DirectionBackward Direction = "backward"
	DirectionForward  Direction = "forward"
)

// Caps holds configured query limits.
type Caps struct {
	MaxLimit     int
	MaxRange     time.Duration
	DefaultSince time.Duration
}

// DefaultCaps returns step-default caps (limit 1000, range 24h).
func DefaultCaps() Caps {
	return Caps{
		MaxLimit:     1000,
		MaxRange:     24 * time.Hour,
		DefaultSince: time.Hour,
	}
}

// Filters are validated query parameters for GET /v1/logs.
type Filters struct {
	Project    string
	Deployment string
	Service    string
	RequestID  string
	TraceID    string
	Q          string
	Since      time.Time
	Until      time.Time
	Limit      int
	Direction  Direction
	Cursor     string

	// Warnings populated when values were clamped.
	Warnings []string
	Capped   bool
}

// ValidateAndNormalize parses raw query values into Filters.
// Requires at least one scoping filter: project, deployment, service,
// request_id, or trace_id.
func ValidateAndNormalize(
	project, deployment, service, requestID, traceID, q, since, until, limit, direction, cursor string,
	now time.Time,
	caps Caps,
) (Filters, error) {
	if caps.MaxLimit < 1 {
		caps.MaxLimit = 1000
	}
	if caps.MaxRange <= 0 {
		caps.MaxRange = 24 * time.Hour
	}
	if caps.DefaultSince <= 0 {
		caps.DefaultSince = time.Hour
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	f := Filters{
		Project:    strings.TrimSpace(project),
		Deployment: strings.TrimSpace(deployment),
		Service:    strings.TrimSpace(service),
		RequestID:  strings.TrimSpace(requestID),
		TraceID:    strings.TrimSpace(traceID),
		Q:          strings.TrimSpace(q),
		Cursor:     strings.TrimSpace(cursor),
		Direction:  DirectionBackward,
	}

	if f.Project == "" && f.Deployment == "" && f.Service == "" && f.RequestID == "" && f.TraceID == "" {
		return Filters{}, fmt.Errorf("at least one scoping filter is required (project, deployment, service, request_id, or trace_id)")
	}

	dir := strings.ToLower(strings.TrimSpace(direction))
	switch dir {
	case "", "backward", "backwards":
		f.Direction = DirectionBackward
	case "forward", "forwards":
		f.Direction = DirectionForward
	default:
		return Filters{}, fmt.Errorf("direction must be forward or backward")
	}

	untilT := now
	if strings.TrimSpace(until) != "" {
		t, err := parseTime(until)
		if err != nil {
			return Filters{}, fmt.Errorf("until: %w", err)
		}
		untilT = t
	}
	sinceT := untilT.Add(-caps.DefaultSince)
	if strings.TrimSpace(since) != "" {
		t, err := parseTime(since)
		if err != nil {
			return Filters{}, fmt.Errorf("since: %w", err)
		}
		sinceT = t
	}
	if !sinceT.Before(untilT) {
		return Filters{}, fmt.Errorf("since must be before until")
	}
	if untilT.Sub(sinceT) > caps.MaxRange {
		sinceT = untilT.Add(-caps.MaxRange)
		f.Capped = true
		f.Warnings = append(f.Warnings, fmt.Sprintf("time range clamped to %s", caps.MaxRange))
	}
	f.Since = sinceT.UTC()
	f.Until = untilT.UTC()

	lim := 100
	if strings.TrimSpace(limit) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(limit))
		if err != nil || n < 1 {
			return Filters{}, fmt.Errorf("limit must be a positive integer")
		}
		lim = n
	}
	if lim > caps.MaxLimit {
		lim = caps.MaxLimit
		f.Capped = true
		f.Warnings = append(f.Warnings, fmt.Sprintf("limit clamped to %d", caps.MaxLimit))
	}
	f.Limit = lim

	return f, nil
}

func parseTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		// Heuristic: ns vs ms vs s.
		switch {
		case n > 1e18:
			return time.Unix(0, n).UTC(), nil
		case n > 1e14:
			return time.Unix(0, n*int64(time.Millisecond)).UTC(), nil
		case n > 1e11:
			return time.UnixMilli(n).UTC(), nil
		default:
			return time.Unix(n, 0).UTC(), nil
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		// Relative durations like "1h" mean "now - duration" for since-style values.
		return time.Now().UTC().Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q (want RFC3339 or unix)", raw)
}

// BuildLogQL translates filters into a Loki LogQL query.
//
// Prefer indexed stream labels (forge_project / forge_deployment / forge_service)
// when present — matching the 12.04 architecture sketch:
//
//	{forge_project="P"} | json | trace_id="T"
//
// When no stream labels apply (trace_id / request_id only), fall back to
// {job=~".+"} and filter via the JSON pipeline. After `| json`, dotted log
// keys like forge.project are available as forge_project.
func BuildLogQL(f Filters) string {
	var selectors []string
	if f.Project != "" {
		selectors = append(selectors, fmt.Sprintf("forge_project=%s", quoteLogQL(f.Project)))
	}
	if f.Deployment != "" {
		selectors = append(selectors, fmt.Sprintf("forge_deployment=%s", quoteLogQL(f.Deployment)))
	}
	if f.Service != "" {
		selectors = append(selectors, fmt.Sprintf("forge_service=%s", quoteLogQL(f.Service)))
	}
	stream := `{job=~".+"}`
	if len(selectors) > 0 {
		stream = "{" + strings.Join(selectors, ",") + "}"
	}

	parts := []string{stream, "| json"}
	if f.TraceID != "" {
		parts = append(parts, fmt.Sprintf("| trace_id=%s", quoteLogQL(f.TraceID)))
	}
	if f.RequestID != "" {
		parts = append(parts, fmt.Sprintf("| request_id=%s", quoteLogQL(f.RequestID)))
	}
	if f.Q != "" {
		parts = append(parts, fmt.Sprintf("|~ %s", quoteLogQL("(?i)"+escapeRegexp(f.Q))))
	}
	return strings.Join(parts, " ")
}

// quoteLogQL returns a double-quoted LogQL string literal.
func quoteLogQL(s string) string {
	return strconv.Quote(s)
}

// escapeRegexp escapes free-text q so |~ treats it as a literal substring.
func escapeRegexp(s string) string {
	special := `\.^$|?*+()[]{}`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
