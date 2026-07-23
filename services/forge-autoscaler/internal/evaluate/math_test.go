package evaluate_test

import (
	"testing"

	"forge.local/services/forge-autoscaler/internal/evaluate"
)

func TestDesiredFromUtilization(t *testing.T) {
	cases := []struct {
		name    string
		current int
		metric  float64
		target  float64
		want    int
	}{
		{name: "scale up from 2 at 130% of 65", current: 2, metric: 130, target: 65, want: 4},
		{name: "scale up ceil", current: 3, metric: 80, target: 65, want: 4},
		{name: "hold near target", current: 4, metric: 65, target: 65, want: 4},
		{name: "scale down", current: 4, metric: 20, target: 65, want: 2},
		{name: "zero current under target", current: 0, metric: 10, target: 65, want: 0},
		{name: "zero current over target", current: 0, metric: 80, target: 65, want: 1},
		{name: "invalid target", current: 3, metric: 80, target: 0, want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluate.DesiredFromUtilization(tc.current, tc.metric, tc.target)
			if got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestClampAndRateLimit(t *testing.T) {
	if got := evaluate.ClampReplicas(0, 2, 10); got != 2 {
		t.Fatalf("clamp min: got %d", got)
	}
	if got := evaluate.ClampReplicas(99, 2, 10); got != 10 {
		t.Fatalf("clamp max: got %d", got)
	}
	if got := evaluate.LimitReplicaDelta(2, 10, 4); got != 6 {
		t.Fatalf("rate limit up: got %d", got)
	}
	if got := evaluate.LimitReplicaDelta(10, 2, 2); got != 8 {
		t.Fatalf("rate limit down: got %d", got)
	}
	if got := evaluate.LimitReplicaDelta(5, 7, 0); got != 7 {
		t.Fatalf("unlimited: got %d", got)
	}
}
