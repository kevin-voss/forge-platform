package metrics_test

import (
	"context"
	"testing"

	"forge.local/services/forge-autoscaler/internal/metrics"
	"forge.local/services/forge-autoscaler/internal/policy"
)

func TestFakeSourceFetchDeterministicAndExhausts(t *testing.T) {
	fake := metrics.NewFakeSource()
	target := policy.TargetRef{Kind: "Application", Name: "invoice-api"}
	fake.Push(target, "cpu", 81.4, 70.0)

	metric := policy.MetricSpec{Type: "cpu", TargetAverageUtilization: floatPtr(65)}
	s1, err := fake.Fetch(context.Background(), target, metric)
	if err != nil {
		t.Fatal(err)
	}
	if s1.Value != 81.4 || s1.Target != 65 {
		t.Fatalf("unexpected sample: %+v", s1)
	}
	s2, err := fake.Fetch(context.Background(), target, metric)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Value != 70.0 {
		t.Fatalf("expected second scripted value, got %v", s2.Value)
	}
	_, err = fake.Fetch(context.Background(), target, metric)
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
}

func floatPtr(v float64) *float64 { return &v }
