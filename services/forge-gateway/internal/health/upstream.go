package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"forge.local/services/forge-gateway/internal/routes"
)

// UpstreamState is the balancer-visible readiness of one upstream.
type UpstreamState string

const (
	UpstreamReady   UpstreamState = "ready"
	UpstreamUnready UpstreamState = "unready"
)

// TransitionReason explains why an upstream changed readiness.
type TransitionReason string

const (
	ReasonSync    TransitionReason = "sync"
	ReasonProbe   TransitionReason = "probe"
	ReasonPassive TransitionReason = "passive"
)

// UpstreamConfig controls active probing and threshold dampening.
type UpstreamConfig struct {
	ProbeInterval      time.Duration
	ProbePath          string
	FailureThreshold   int
	SuccessThreshold   int
	TrustRuntimeStatus bool
	ProbeTimeout       time.Duration
}

// DefaultUpstreamConfig returns production defaults from the step contract.
func DefaultUpstreamConfig() UpstreamConfig {
	return UpstreamConfig{
		ProbeInterval:      5 * time.Second,
		ProbePath:          "/health/ready",
		FailureThreshold:   3,
		SuccessThreshold:   2,
		TrustRuntimeStatus: true,
		ProbeTimeout:       2 * time.Second,
	}
}

// UpstreamTracker holds per-upstream ready/unready state with thresholds.
type UpstreamTracker struct {
	cfg    UpstreamConfig
	log    *slog.Logger
	client *http.Client

	mu      sync.RWMutex
	entries map[string]*upstreamEntry
}

type upstreamEntry struct {
	state                UpstreamState
	consecutiveFailures  int
	consecutiveSuccesses int
	lastProbeAt          time.Time
	lastChange           time.Time
}

// SyncUpstream is one upstream URL with optional Runtime/Control readiness.
// Ready == nil means the sync source did not provide an authoritative signal.
type SyncUpstream struct {
	URL   string
	Ready *bool
}

// NewUpstreamTracker constructs a tracker. Nil log uses slog.Default().
func NewUpstreamTracker(cfg UpstreamConfig, log *slog.Logger) *UpstreamTracker {
	if log == nil {
		log = slog.Default()
	}
	if cfg.ProbePath == "" {
		cfg.ProbePath = "/health/ready"
	}
	if !strings.HasPrefix(cfg.ProbePath, "/") {
		cfg.ProbePath = "/" + cfg.ProbePath
	}
	if cfg.FailureThreshold < 1 {
		cfg.FailureThreshold = 1
	}
	if cfg.SuccessThreshold < 1 {
		cfg.SuccessThreshold = 1
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 2 * time.Second
	}
	return &UpstreamTracker{
		cfg: cfg,
		log: log,
		client: &http.Client{
			Timeout: cfg.ProbeTimeout,
		},
		entries: make(map[string]*upstreamEntry),
	}
}

// IsReady reports whether traffic may be sent to upstreamURL.
// Unknown upstreams are treated as ready so admin/static routes keep working
// until probe/passive evidence accumulates.
func (t *UpstreamTracker) IsReady(upstreamURL string) bool {
	if t == nil {
		return true
	}
	key := normalizeUpstreamURL(upstreamURL)
	if key == "" {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[key]
	if !ok {
		return true
	}
	return e.state == UpstreamReady
}

// FilterReady returns upstreams that are currently ready for traffic.
func (t *UpstreamTracker) FilterReady(upstreams []routes.Upstream) []routes.Upstream {
	if t == nil {
		out := make([]routes.Upstream, len(upstreams))
		copy(out, upstreams)
		return out
	}
	out := make([]routes.Upstream, 0, len(upstreams))
	for _, u := range upstreams {
		if t.IsReady(u.URL) {
			out = append(out, u)
		}
	}
	return out
}

// ApplySync feeds Runtime/Control readiness into the tracker.
// When TrustRuntimeStatus is true, Ready is applied authoritatively.
// Always reconciles the known URL set (prunes removed upstreams).
func (t *UpstreamTracker) ApplySync(upstreams []SyncUpstream) {
	if t == nil {
		return
	}
	keep := make(map[string]struct{}, len(upstreams))
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC()
	for _, u := range upstreams {
		key := normalizeUpstreamURL(u.URL)
		if key == "" {
			continue
		}
		keep[key] = struct{}{}
		e := t.ensureLocked(key, now)
		if t.cfg.TrustRuntimeStatus && u.Ready != nil {
			desired := UpstreamUnready
			if *u.Ready {
				desired = UpstreamReady
			}
			t.setStateLocked(key, e, desired, ReasonSync, now)
			e.consecutiveFailures = 0
			e.consecutiveSuccesses = 0
		}
	}
	for key := range t.entries {
		if _, ok := keep[key]; !ok {
			delete(t.entries, key)
		}
	}
}

// Reconcile ensures URLs exist (default ready) and prunes unknowns.
// Used after admin route replace when there is no sync readiness signal.
func (t *UpstreamTracker) Reconcile(urls []string) {
	if t == nil {
		return
	}
	keep := make(map[string]struct{}, len(urls))
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC()
	for _, raw := range urls {
		key := normalizeUpstreamURL(raw)
		if key == "" {
			continue
		}
		keep[key] = struct{}{}
		_ = t.ensureLocked(key, now)
	}
	for key := range t.entries {
		if _, ok := keep[key]; !ok {
			delete(t.entries, key)
		}
	}
}

// RecordPassiveFailure counts a connection error or upstream 5xx toward unready.
func (t *UpstreamTracker) RecordPassiveFailure(upstreamURL string) {
	t.recordOutcome(upstreamURL, false, ReasonPassive)
}

// RecordPassiveSuccess counts a non-5xx proxied response toward ready.
func (t *UpstreamTracker) RecordPassiveSuccess(upstreamURL string) {
	t.recordOutcome(upstreamURL, true, ReasonPassive)
}

// RecordProbeFailure counts a failed active readiness probe.
func (t *UpstreamTracker) RecordProbeFailure(upstreamURL string) {
	t.recordOutcome(upstreamURL, false, ReasonProbe)
}

// RecordProbeSuccess counts a successful active readiness probe.
func (t *UpstreamTracker) RecordProbeSuccess(upstreamURL string) {
	t.recordOutcome(upstreamURL, true, ReasonProbe)
}

func (t *UpstreamTracker) recordOutcome(upstreamURL string, success bool, reason TransitionReason) {
	if t == nil {
		return
	}
	key := normalizeUpstreamURL(upstreamURL)
	if key == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC()
	e := t.ensureLocked(key, now)
	if reason == ReasonProbe {
		e.lastProbeAt = now
	}
	if success {
		e.consecutiveSuccesses++
		e.consecutiveFailures = 0
		if e.state != UpstreamReady && e.consecutiveSuccesses >= t.cfg.SuccessThreshold {
			t.setStateLocked(key, e, UpstreamReady, reason, now)
		}
		return
	}
	e.consecutiveFailures++
	e.consecutiveSuccesses = 0
	if e.state != UpstreamUnready && e.consecutiveFailures >= t.cfg.FailureThreshold {
		t.setStateLocked(key, e, UpstreamUnready, reason, now)
	}
}

// Snapshot returns a copy of tracker state for tests/diagnostics.
func (t *UpstreamTracker) Snapshot() map[string]UpstreamState {
	out := make(map[string]UpstreamState)
	if t == nil {
		return out
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for k, e := range t.entries {
		out[k] = e.state
	}
	return out
}

// RunProber periodically GET{upstream}{probePath} for every known upstream.
// Interval <= 0 disables the loop.
func (t *UpstreamTracker) RunProber(ctx context.Context, table *routes.Table) {
	if t == nil || t.cfg.ProbeInterval <= 0 {
		return
	}
	t.log.Info("upstream probe loop started",
		"interval_seconds", int(t.cfg.ProbeInterval.Seconds()),
		"probe_path", t.cfg.ProbePath,
		"failure_threshold", t.cfg.FailureThreshold,
		"success_threshold", t.cfg.SuccessThreshold,
		"trust_runtime_status", t.cfg.TrustRuntimeStatus,
	)
	// Immediate first pass so recovery/unready is not delayed a full interval.
	t.probeAll(ctx, table)

	ticker := time.NewTicker(t.cfg.ProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.log.Info("upstream probe loop stopped")
			return
		case <-ticker.C:
			t.probeAll(ctx, table)
		}
	}
}

func (t *UpstreamTracker) probeAll(ctx context.Context, table *routes.Table) {
	urls := t.knownURLs(table)
	for _, u := range urls {
		if ctx.Err() != nil {
			return
		}
		ok := t.probeOne(ctx, u)
		if ok {
			t.RecordProbeSuccess(u)
		} else {
			t.RecordProbeFailure(u)
		}
	}
}

func (t *UpstreamTracker) knownURLs(table *routes.Table) []string {
	seen := make(map[string]struct{})
	var out []string
	if table != nil {
		for _, r := range table.Snapshot() {
			for _, u := range r.Upstreams {
				key := normalizeUpstreamURL(u.URL)
				if key == "" {
					continue
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, key)
			}
		}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for key := range t.entries {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func (t *UpstreamTracker) probeOne(ctx context.Context, upstreamURL string) bool {
	probeURL, err := joinProbeURL(upstreamURL, t.cfg.ProbePath)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return false
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Status only — do not log response bodies.
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (t *UpstreamTracker) ensureLocked(key string, now time.Time) *upstreamEntry {
	e, ok := t.entries[key]
	if ok {
		return e
	}
	e = &upstreamEntry{
		state:      UpstreamReady,
		lastChange: now,
	}
	t.entries[key] = e
	return e
}

func (t *UpstreamTracker) setStateLocked(key string, e *upstreamEntry, desired UpstreamState, reason TransitionReason, now time.Time) {
	if e.state == desired {
		return
	}
	prev := e.state
	e.state = desired
	e.lastChange = now
	t.log.Info("upstream state transition",
		"upstream", key,
		"from", string(prev),
		"to", string(desired),
		"reason", string(reason),
	)
}

func normalizeUpstreamURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	u.Scheme = strings.ToLower(u.Scheme)
	u.Fragment = ""
	// Drop default path noise; keep path if non-root (unusual for upstreams).
	if u.Path == "/" {
		u.Path = ""
	}
	return strings.TrimRight(u.String(), "/")
}

func joinProbeURL(upstreamURL, probePath string) (string, error) {
	normalized := normalizeUpstreamURL(upstreamURL)
	if normalized == "" {
		return "", fmt.Errorf("invalid upstream url")
	}
	base, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid upstream url")
	}
	ref, err := url.Parse(probePath)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}
