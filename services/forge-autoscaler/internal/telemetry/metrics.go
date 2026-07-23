package telemetry

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry holds process-local autoscaler metrics for /metrics exposition.
type Registry struct {
	mu              sync.Mutex
	recommendations map[string]*atomic.Int64
	scaleActions    map[string]*atomic.Int64
	sourceLatency   map[string]*atomic.Uint64 // last latency in microseconds
	queueBacklog    map[string]*atomic.Uint64 // float bits via math? store as int64 of backlog
	workerDesired   map[string]*atomic.Int64
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		recommendations: map[string]*atomic.Int64{},
		scaleActions:    map[string]*atomic.Int64{},
		sourceLatency:   map[string]*atomic.Uint64{},
		queueBacklog:    map[string]*atomic.Uint64{},
		workerDesired:   map[string]*atomic.Int64{},
	}
}

// SetRecommendationReplicas sets forge_autoscaler_recommendation_replicas.
func (r *Registry) SetRecommendationReplicas(policy, targetKind, targetName string, replicas int) {
	if r == nil {
		return
	}
	key := labelsKey("policy", policy, "target_kind", targetKind, "target_name", targetName)
	r.mu.Lock()
	gauge, ok := r.recommendations[key]
	if !ok {
		gauge = &atomic.Int64{}
		r.recommendations[key] = gauge
	}
	r.mu.Unlock()
	gauge.Store(int64(replicas))
}

// IncScaleAction increments forge_autoscaler_scale_actions_total.
func (r *Registry) IncScaleAction(direction, result string) {
	if r == nil {
		return
	}
	key := labelsKey("direction", direction, "result", result)
	r.mu.Lock()
	counter, ok := r.scaleActions[key]
	if !ok {
		counter = &atomic.Int64{}
		r.scaleActions[key] = counter
	}
	r.mu.Unlock()
	counter.Add(1)
}

// ObserveSourceLatency records forge_autoscaler_metric_source_latency_seconds{source}.
func (r *Registry) ObserveSourceLatency(source string, seconds float64) {
	if r == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	key := labelsKey("source", source)
	micros := uint64(seconds * 1_000_000)
	r.mu.Lock()
	gauge, ok := r.sourceLatency[key]
	if !ok {
		gauge = &atomic.Uint64{}
		r.sourceLatency[key] = gauge
	}
	r.mu.Unlock()
	gauge.Store(micros)
}

// SetQueueBacklog sets forge_autoscaler_queue_backlog{queue}.
func (r *Registry) SetQueueBacklog(queue string, backlog float64) {
	if r == nil {
		return
	}
	if backlog < 0 {
		backlog = 0
	}
	key := labelsKey("queue", queue)
	// Store milli-units so fractional depths remain visible in exposition.
	millis := uint64(backlog * 1000)
	r.mu.Lock()
	gauge, ok := r.queueBacklog[key]
	if !ok {
		gauge = &atomic.Uint64{}
		r.queueBacklog[key] = gauge
	}
	r.mu.Unlock()
	gauge.Store(millis)
}

// SetWorkerDesiredReplicas sets forge_autoscaler_worker_desired_replicas{worker}.
func (r *Registry) SetWorkerDesiredReplicas(worker string, replicas int) {
	if r == nil {
		return
	}
	key := labelsKey("worker", worker)
	r.mu.Lock()
	gauge, ok := r.workerDesired[key]
	if !ok {
		gauge = &atomic.Int64{}
		r.workerDesired[key] = gauge
	}
	r.mu.Unlock()
	gauge.Store(int64(replicas))
}

// Handler serves Prometheus text exposition.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder
		b.WriteString("# HELP forge_autoscaler_recommendation_replicas Latest recommended replica count.\n")
		b.WriteString("# TYPE forge_autoscaler_recommendation_replicas gauge\n")
		r.mu.Lock()
		recKeys := sortedKeys(r.recommendations)
		for _, key := range recKeys {
			fmt.Fprintf(&b, "forge_autoscaler_recommendation_replicas%s %d\n", key, r.recommendations[key].Load())
		}
		b.WriteString("# HELP forge_autoscaler_scale_actions_total Scale actuation attempts.\n")
		b.WriteString("# TYPE forge_autoscaler_scale_actions_total counter\n")
		actKeys := sortedKeys(r.scaleActions)
		for _, key := range actKeys {
			fmt.Fprintf(&b, "forge_autoscaler_scale_actions_total%s %d\n", key, r.scaleActions[key].Load())
		}
		b.WriteString("# HELP forge_autoscaler_metric_source_latency_seconds Last metric-source fetch latency in seconds.\n")
		b.WriteString("# TYPE forge_autoscaler_metric_source_latency_seconds gauge\n")
		latKeys := sortedUintKeys(r.sourceLatency)
		for _, key := range latKeys {
			secs := float64(r.sourceLatency[key].Load()) / 1_000_000
			fmt.Fprintf(&b, "forge_autoscaler_metric_source_latency_seconds%s %g\n", key, secs)
		}
		b.WriteString("# HELP forge_autoscaler_queue_backlog Latest observed queue backlog depth.\n")
		b.WriteString("# TYPE forge_autoscaler_queue_backlog gauge\n")
		backlogKeys := sortedUintKeys(r.queueBacklog)
		for _, key := range backlogKeys {
			depth := float64(r.queueBacklog[key].Load()) / 1000
			fmt.Fprintf(&b, "forge_autoscaler_queue_backlog%s %g\n", key, depth)
		}
		b.WriteString("# HELP forge_autoscaler_worker_desired_replicas Latest desired worker replica count.\n")
		b.WriteString("# TYPE forge_autoscaler_worker_desired_replicas gauge\n")
		workerKeys := sortedKeys(r.workerDesired)
		for _, key := range workerKeys {
			fmt.Fprintf(&b, "forge_autoscaler_worker_desired_replicas%s %d\n", key, r.workerDesired[key].Load())
		}
		r.mu.Unlock()
		_, _ = w.Write([]byte(b.String()))
	})
}

func labelsKey(parts ...string) string {
	if len(parts)%2 != 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < len(parts); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, parts[i], escapeLabel(parts[i+1]))
	}
	b.WriteByte('}')
	return b.String()
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func sortedKeys(m map[string]*atomic.Int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedUintKeys(m map[string]*atomic.Uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
