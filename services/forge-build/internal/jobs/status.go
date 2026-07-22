package jobs

import "fmt"

// Status is the high-level lifecycle state of a build job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// Phase is the detailed progress within a build.
type Phase string

const (
	PhaseQueued    Phase = "queued"
	PhaseCloning   Phase = "cloning"
	PhaseBuilding  Phase = "building"
	PhasePushing   Phase = "pushing"
	PhaseSucceeded Phase = "succeeded"
	PhaseFailed    Phase = "failed"
	PhaseCanceled  Phase = "canceled"
)

// Error codes for structured build failures.
const (
	ErrCodeCloneFailed     = "clone_failed"
	ErrCodeManifestInvalid = "manifest_invalid"
	ErrCodeBuildFailed     = "build_failed"
	ErrCodeBuildTimeout    = "build_timeout"
	ErrCodePushFailed      = "push_failed"
	ErrCodeInterrupted     = "interrupted"
	ErrCodeCanceled        = "canceled"
	ErrCodeWorkspace       = "workspace_error"
	ErrCodeTagFailed       = "tag_failed"
	ErrCodeShutdown        = "shutdown"
)

// BuildError is a structured failure detail.
type BuildError struct {
	Code    string
	Message string
}

// IsTerminal reports whether status is a terminal lifecycle state.
func IsTerminal(s Status) bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

// ImageInvariantOK reports whether image/digest presence matches succeeded.
func ImageInvariantOK(status Status, image, digest string) bool {
	hasImage := image != ""
	if status == StatusSucceeded {
		return hasImage
	}
	return !hasImage && digest == ""
}

// ValidateTransition checks whether a status/phase change is legal.
func ValidateTransition(fromStatus Status, fromPhase Phase, toStatus Status, toPhase Phase) error {
	if fromStatus == toStatus && fromPhase == toPhase {
		return nil
	}
	if IsTerminal(fromStatus) {
		return fmt.Errorf("illegal transition: %s/%s → %s/%s (already terminal)", fromStatus, fromPhase, toStatus, toPhase)
	}

	switch {
	case fromStatus == StatusQueued && fromPhase == PhaseQueued &&
		toStatus == StatusRunning && toPhase == PhaseCloning:
		return nil
	case fromStatus == StatusQueued && fromPhase == PhaseQueued &&
		toStatus == StatusCanceled && toPhase == PhaseCanceled:
		return nil
	case fromStatus == StatusQueued && fromPhase == PhaseQueued &&
		toStatus == StatusFailed && toPhase == PhaseFailed:
		return nil

	case fromStatus == StatusRunning && fromPhase == PhaseCloning &&
		toStatus == StatusRunning && toPhase == PhaseBuilding:
		return nil
	case fromStatus == StatusRunning && fromPhase == PhaseBuilding &&
		toStatus == StatusRunning && toPhase == PhasePushing:
		return nil

	case fromStatus == StatusRunning &&
		(fromPhase == PhaseCloning || fromPhase == PhaseBuilding || fromPhase == PhasePushing) &&
		toStatus == StatusSucceeded && toPhase == PhaseSucceeded:
		return nil
	case fromStatus == StatusRunning &&
		(fromPhase == PhaseCloning || fromPhase == PhaseBuilding || fromPhase == PhasePushing) &&
		toStatus == StatusFailed && toPhase == PhaseFailed:
		return nil
	case fromStatus == StatusRunning &&
		(fromPhase == PhaseCloning || fromPhase == PhaseBuilding || fromPhase == PhasePushing) &&
		toStatus == StatusCanceled && toPhase == PhaseCanceled:
		return nil
	}

	return fmt.Errorf("illegal transition: %s/%s → %s/%s", fromStatus, fromPhase, toStatus, toPhase)
}
