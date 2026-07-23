package controller

// Node.status.phase values (23.03).
const (
	PhaseProvisioning  = "Provisioning"
	PhaseBootstrapping = "Bootstrapping"
	PhaseJoining       = "Joining"
	PhaseReady         = "Ready"
	PhaseDraining      = "Draining"
	PhaseDeleting      = "Deleting"
	PhaseFailed        = "Failed"
)

// Phase events that drive NextPhase.
const (
	EventMachineBooted  = "MachineBooted"
	EventHealthReady    = "HealthReady"
	EventRegistered     = "Registered"
	EventTimeout        = "Timeout"
	EventDrainRequested = "DrainRequested"
	EventDrainComplete  = "DrainComplete"
	EventDrainTimeout   = "DrainTimeout"
	EventFailedCleanup  = "FailedCleanup"
	EventDeleteDone     = "DeleteDone"
)

// Timeout reasons written into Node.status.conditions.
const (
	ReasonProvisionTimeout = "ProvisionTimeout"
	ReasonBootstrapTimeout = "BootstrapTimeout"
	ReasonJoinTimeout      = "JoinTimeout"
	ReasonDrainTimeout     = "DrainTimeout"
)

// Transition is the result of applying an event to a phase.
type Transition struct {
	To     string
	Reason string
	OK     bool
}

// NextPhase returns the documented next phase for (phase, event).
func NextPhase(phase, event string) Transition {
	switch phase {
	case PhaseProvisioning:
		switch event {
		case EventMachineBooted:
			return Transition{To: PhaseBootstrapping, OK: true}
		case EventTimeout:
			return Transition{To: PhaseFailed, Reason: ReasonProvisionTimeout, OK: true}
		}
	case PhaseBootstrapping:
		switch event {
		case EventHealthReady:
			return Transition{To: PhaseJoining, OK: true}
		case EventTimeout:
			return Transition{To: PhaseFailed, Reason: ReasonBootstrapTimeout, OK: true}
		}
	case PhaseJoining:
		switch event {
		case EventRegistered:
			return Transition{To: PhaseReady, OK: true}
		case EventTimeout:
			return Transition{To: PhaseFailed, Reason: ReasonJoinTimeout, OK: true}
		}
	case PhaseReady:
		if event == EventDrainRequested {
			return Transition{To: PhaseDraining, OK: true}
		}
	case PhaseFailed:
		if event == EventFailedCleanup || event == EventDrainRequested {
			return Transition{To: PhaseDraining, Reason: "FailedCleanup", OK: true}
		}
	case PhaseDraining:
		switch event {
		case EventDrainComplete:
			return Transition{To: PhaseDeleting, OK: true}
		case EventDrainTimeout:
			return Transition{To: PhaseDeleting, Reason: ReasonDrainTimeout, OK: true}
		}
	case PhaseDeleting:
		if event == EventDeleteDone {
			return Transition{To: "", OK: true} // resource removed
		}
	}
	return Transition{OK: false}
}

// TimeoutReasonForPhase returns the timeout condition reason for a pre-Ready phase.
func TimeoutReasonForPhase(phase string) string {
	switch phase {
	case PhaseProvisioning:
		return ReasonProvisionTimeout
	case PhaseBootstrapping:
		return ReasonBootstrapTimeout
	case PhaseJoining:
		return ReasonJoinTimeout
	default:
		return ""
	}
}

// IsPreReady reports whether the phase is still subject to bootstrap timeouts.
func IsPreReady(phase string) bool {
	switch phase {
	case PhaseProvisioning, PhaseBootstrapping, PhaseJoining:
		return true
	default:
		return false
	}
}
