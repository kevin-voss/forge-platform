package jobs_test

import (
	"testing"

	"forge.local/services/forge-build/internal/jobs"
)

func TestValidateTransitionHappyPath(t *testing.T) {
	steps := []struct {
		fromStatus jobs.Status
		fromPhase  jobs.Phase
		toStatus   jobs.Status
		toPhase    jobs.Phase
	}{
		{jobs.StatusQueued, jobs.PhaseQueued, jobs.StatusRunning, jobs.PhaseCloning},
		{jobs.StatusRunning, jobs.PhaseCloning, jobs.StatusRunning, jobs.PhaseBuilding},
		{jobs.StatusRunning, jobs.PhaseBuilding, jobs.StatusRunning, jobs.PhasePushing},
		{jobs.StatusRunning, jobs.PhasePushing, jobs.StatusSucceeded, jobs.PhaseSucceeded},
	}
	for _, s := range steps {
		if err := jobs.ValidateTransition(s.fromStatus, s.fromPhase, s.toStatus, s.toPhase); err != nil {
			t.Fatalf("%s/%s → %s/%s: %v", s.fromStatus, s.fromPhase, s.toStatus, s.toPhase, err)
		}
	}
}

func TestValidateTransitionRejectsIllegal(t *testing.T) {
	cases := []struct {
		name string
		from jobs.Status
		fp   jobs.Phase
		to   jobs.Status
		tp   jobs.Phase
	}{
		{"skip building", jobs.StatusRunning, jobs.PhaseCloning, jobs.StatusRunning, jobs.PhasePushing},
		{"from succeeded", jobs.StatusSucceeded, jobs.PhaseSucceeded, jobs.StatusFailed, jobs.PhaseFailed},
		{"queued to pushing", jobs.StatusQueued, jobs.PhaseQueued, jobs.StatusRunning, jobs.PhasePushing},
		{"running to queued", jobs.StatusRunning, jobs.PhaseBuilding, jobs.StatusQueued, jobs.PhaseQueued},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := jobs.ValidateTransition(tc.from, tc.fp, tc.to, tc.tp); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestImageInvariant(t *testing.T) {
	if !jobs.ImageInvariantOK(jobs.StatusSucceeded, "localhost:5000/x:1", "sha256:abc") {
		t.Fatal("succeeded with image should pass")
	}
	if jobs.ImageInvariantOK(jobs.StatusSucceeded, "", "") {
		t.Fatal("succeeded without image should fail")
	}
	if !jobs.ImageInvariantOK(jobs.StatusFailed, "", "") {
		t.Fatal("failed without image should pass")
	}
	if jobs.ImageInvariantOK(jobs.StatusFailed, "img", "") {
		t.Fatal("failed with image should fail")
	}
	if jobs.ImageInvariantOK(jobs.StatusCanceled, "", "sha256:x") {
		t.Fatal("canceled with digest should fail")
	}
}

func TestCancelFromQueuedIsLegal(t *testing.T) {
	if err := jobs.ValidateTransition(jobs.StatusQueued, jobs.PhaseQueued, jobs.StatusCanceled, jobs.PhaseCanceled); err != nil {
		t.Fatal(err)
	}
}
