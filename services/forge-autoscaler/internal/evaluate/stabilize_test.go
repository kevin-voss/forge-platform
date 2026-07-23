package evaluate_test

import (
	"testing"
	"time"

	"forge.local/services/forge-autoscaler/internal/evaluate"
)

func TestStabilizerPreventsRapidDownscale(t *testing.T) {
	s := evaluate.NewStabilizer()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	key := "demo/production/p1"

	// Establish current high recommendation of 6.
	_ = s.Apply(key, 6, 6, 0, 60*time.Second, now)
	// A sudden drop to 2 must not apply immediately while 6 is still in the window.
	got := s.Apply(key, 6, 2, 0, 60*time.Second, now.Add(5*time.Second))
	if got != 6 {
		t.Fatalf("expected scale-down hold at 6, got %d", got)
	}

	// After the high sample ages out of the window, downscale proceeds.
	got2 := s.Apply(key, 6, 2, 0, 60*time.Second, now.Add(70*time.Second))
	if got2 != 2 {
		t.Fatalf("after window clear got %d want 2", got2)
	}
}

func TestStabilizerScaleUpTakesMax(t *testing.T) {
	s := evaluate.NewStabilizer()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	key := "demo/production/up"

	_ = s.Apply(key, 2, 3, 30*time.Second, 0, now)
	got := s.Apply(key, 2, 5, 30*time.Second, 0, now.Add(5*time.Second))
	if got != 5 {
		t.Fatalf("scale-up should take max in window, got %d", got)
	}

	gotImmediate := s.Apply(key+"-fast", 2, 8, 0, 0, now)
	if gotImmediate != 8 {
		t.Fatalf("zero window: got %d", gotImmediate)
	}
}
