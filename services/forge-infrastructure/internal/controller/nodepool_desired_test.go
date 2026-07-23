package controller

import (
	"testing"

	"forge.local/services/forge-infrastructure/internal/registryclient"
)

func TestDesiredReplicasFromPool(t *testing.T) {
	pool := registryclient.Resource{
		Spec:   map[string]any{"replicas": 2},
		Status: map[string]any{"desiredNodes": 4},
	}
	if got := desiredReplicasFromPool(pool); got != 4 {
		t.Fatalf("got %d want 4", got)
	}
	pool2 := registryclient.Resource{Spec: map[string]any{"replicas": 3}}
	if got := desiredReplicasFromPool(pool2); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
}

func TestSelectScaleDownVictimPrefersDrainCandidate(t *testing.T) {
	nodes := []registryclient.Resource{
		{Metadata: registryclient.Metadata{Name: "node-z"}, Status: map[string]any{"runtimeNodeId": "runtime-z", "phase": "Ready"}},
		{Metadata: registryclient.Metadata{Name: "node-a"}, Status: map[string]any{"runtimeNodeId": "runtime-a", "phase": "Ready"}},
	}
	status := map[string]any{
		"drainCandidateNodeId": "runtime-a",
		"drainCandidates":      []any{"runtime-a"},
	}
	victim := selectScaleDownVictim(nodes, status)
	if victim == nil || victim.Metadata.Name != "node-a" {
		t.Fatalf("victim=%v", victim)
	}
	// Fallback when no candidate matches.
	fallback := selectScaleDownVictim(nodes, map[string]any{})
	if fallback == nil || fallback.Metadata.Name != "node-z" {
		t.Fatalf("fallback=%v", fallback)
	}
}

func TestMergePreserveAutoscalerStatus(t *testing.T) {
	existing := map[string]any{
		"desiredNodes":           3,
		"lastScaleUpOperationId": "scaleup-pool-abc",
		"conditions": []map[string]any{
			{"type": "ScaleUpRecommended", "status": "True", "reason": "scale_up"},
		},
	}
	next := map[string]any{
		"phase":      "Ready",
		"readyNodes": 2,
		"conditions": []map[string]any{
			{"type": "ProviderConfigured", "status": "True", "reason": "ProviderResolved"},
		},
	}
	merged := mergePreserveAutoscalerStatus(existing, next, 3, 2)
	if merged["desiredNodes"] != 3 {
		t.Fatalf("desiredNodes=%v", merged["desiredNodes"])
	}
	if merged["lastScaleUpOperationId"] != "scaleup-pool-abc" {
		t.Fatalf("op=%v", merged["lastScaleUpOperationId"])
	}
	conds := conditionsFromStatus(merged)
	types := map[string]bool{}
	for _, c := range conds {
		types[stringFromSpec(c, "type")] = true
	}
	if !types["ScaleUpRecommended"] || !types["ProviderConfigured"] {
		t.Fatalf("conditions=%#v", conds)
	}
}
