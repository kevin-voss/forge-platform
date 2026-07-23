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
