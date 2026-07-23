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
}

// NewRegistry creates an empty metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		recommendations: map[string]*atomic.Int64{},
		scaleActions:    map[string]*atomic.Int64{},
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
