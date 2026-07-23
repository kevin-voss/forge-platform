package controller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"forge.local/services/forge-infrastructure/internal/operations"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/registryclient"
)

// NodeRegistry is the registry subset used by NodeController.
type NodeRegistry interface {
	Get(ctx context.Context, plural, name string) (*registryclient.Resource, error)
	PutStatus(ctx context.Context, plural, name, resourceVersion string, status map[string]any) (*registryclient.Resource, error)
	Delete(ctx context.Context, plural, name string) error
}

// NodeTimeouts holds per-phase deadline durations.
type NodeTimeouts struct {
	Provision time.Duration
	Bootstrap time.Duration
	Join      time.Duration
	Drain     time.Duration
}

// NodeController drives Node.status.phase through bootstrap/join/drain/delete.
type NodeController struct {
	Registry  NodeRegistry
	Ledger    OpLedger
	Providers ProviderResolver
	Timers    TimerStore
	Drain     DrainHook
	Events    EventPublisher
	Machines  MachineObserver
	Health    HealthProber
	Join      JoinObserver
	Timeouts  NodeTimeouts
	Log       *slog.Logger
	Clock     func() time.Time

	// ResolveProvider returns the provider adapter + ledger providerRef for a NodePool.
	ResolveProvider func(ctx context.Context, poolName string) (prov provider.Provider, providerRef string, err error)
}

func (c *NodeController) now() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

// Reconcile advances a single Node through the phase state machine.
func (c *NodeController) Reconcile(ctx context.Context, node registryclient.Resource) error {
	phase := stringFromStatus(node.Status, "phase")
	if phase == "" {
		phase = PhaseProvisioning
	}
	nodeID := node.Metadata.ID
	if nodeID == "" {
		nodeID = node.Metadata.Name
	}
	pool := stringFromSpec(node.Spec, "nodePoolRef")
	providerNodeID := stringFromSpec(node.Spec, "providerNodeId")
	address := stringFromStatus(node.Status, "address")
	if address == "" {
		address = stringFromSpec(node.Spec, "address")
	}
	runtimeNodeID := stringFromStatus(node.Status, "runtimeNodeId")

	if err := c.ensureTimer(ctx, nodeID, phase); err != nil {
		return err
	}

	switch phase {
	case PhaseProvisioning:
		return c.reconcileProvisioning(ctx, node, nodeID, pool, providerNodeID)
	case PhaseBootstrapping:
		return c.reconcileBootstrapping(ctx, node, nodeID, pool, address)
	case PhaseJoining:
		return c.reconcileJoining(ctx, node, nodeID, pool, address)
	case PhaseReady:
		return nil
	case PhaseFailed:
		return c.transition(ctx, node, nodeID, pool, phase, EventFailedCleanup, "")
	case PhaseDraining:
		return c.reconcileDraining(ctx, node, nodeID, pool, runtimeNodeID)
	case PhaseDeleting:
		return c.reconcileDeleting(ctx, node, nodeID, pool, providerNodeID)
	default:
		return nil
	}
}

// RequestDrain moves a node into Draining (scale-down / cleanup).
func (c *NodeController) RequestDrain(ctx context.Context, node registryclient.Resource) error {
	phase := stringFromStatus(node.Status, "phase")
	nodeID := node.Metadata.ID
	if nodeID == "" {
		nodeID = node.Metadata.Name
	}
	pool := stringFromSpec(node.Spec, "nodePoolRef")
	switch phase {
	case PhaseDraining, PhaseDeleting:
		return nil
	case PhaseFailed:
		return c.transition(ctx, node, nodeID, pool, phase, EventFailedCleanup, "ScaleDown")
	default:
		return c.transition(ctx, node, nodeID, pool, phase, EventDrainRequested, "ScaleDown")
	}
}

func (c *NodeController) reconcileProvisioning(ctx context.Context, node registryclient.Resource, nodeID, pool, providerNodeID string) error {
	if timedOut, err := c.checkTimeout(ctx, node, nodeID, pool, PhaseProvisioning); timedOut || err != nil {
		return err
	}
	prov, _, err := c.resolveNodeProvider(ctx, pool)
	if err != nil && c.Machines == nil {
		return err
	}
	booted := false
	if c.Machines != nil {
		booted, _ = c.Machines.IsBooted(ctx, prov, providerNodeID)
	}
	if booted {
		return c.transition(ctx, node, nodeID, pool, PhaseProvisioning, EventMachineBooted, "")
	}
	return nil
}

func (c *NodeController) reconcileBootstrapping(ctx context.Context, node registryclient.Resource, nodeID, pool, address string) error {
	if timedOut, err := c.checkTimeout(ctx, node, nodeID, pool, PhaseBootstrapping); timedOut || err != nil {
		return err
	}
	ready := false
	if c.Health != nil {
		ready, _ = c.Health.Ready(ctx, address)
	}
	if ready {
		return c.transition(ctx, node, nodeID, pool, PhaseBootstrapping, EventHealthReady, "")
	}
	return nil
}

func (c *NodeController) reconcileJoining(ctx context.Context, node registryclient.Resource, nodeID, pool, address string) error {
	if timedOut, err := c.checkTimeout(ctx, node, nodeID, pool, PhaseJoining); timedOut || err != nil {
		return err
	}
	if c.Join == nil {
		return nil
	}
	runtimeID, online, err := c.Join.Observe(ctx, address)
	if err != nil {
		return err
	}
	if online && runtimeID != "" {
		if node.Status == nil {
			node.Status = map[string]any{}
		}
		node.Status["runtimeNodeId"] = runtimeID
		return c.transition(ctx, node, nodeID, pool, PhaseJoining, EventRegistered, "")
	}
	return nil
}

func (c *NodeController) reconcileDraining(ctx context.Context, node registryclient.Resource, nodeID, pool, runtimeNodeID string) error {
	now := c.now()
	if c.Timers != nil {
		_ = c.Timers.MarkDrainStarted(ctx, nodeID, now)
	}
	if runtimeNodeID != "" && c.Drain != nil {
		_ = c.Drain.BeginDrain(ctx, runtimeNodeID)
	}

	workloads := []string{}
	if c.Drain != nil && runtimeNodeID != "" {
		var err error
		workloads, err = c.Drain.Workloads(ctx, runtimeNodeID)
		if err != nil && c.Log != nil {
			c.Log.Warn("drain workload lookup failed", "node_id", nodeID, "error", err.Error())
		}
	}

	timer, _ := c.Timers.Get(ctx, nodeID)
	drainDeadline := c.Timeouts.Drain
	if drainDeadline <= 0 {
		drainDeadline = 300 * time.Second
	}
	started := now
	if timer != nil && timer.DrainStartedAt != nil {
		started = *timer.DrainStartedAt
	}
	if len(workloads) == 0 {
		return c.transition(ctx, node, nodeID, pool, PhaseDraining, EventDrainComplete, "")
	}
	if now.Sub(started) >= drainDeadline {
		if c.Log != nil {
			c.Log.Warn("drain timeout; deleting with stranded workloads",
				"node_id", nodeID,
				"node_pool", pool,
				"stranded_workloads", workloads,
				"reason", ReasonDrainTimeout,
			)
		}
		return c.transition(ctx, node, nodeID, pool, PhaseDraining, EventDrainTimeout, ReasonDrainTimeout)
	}
	return nil
}

func (c *NodeController) reconcileDeleting(ctx context.Context, node registryclient.Resource, nodeID, pool, providerNodeID string) error {
	prov, providerRef, err := c.resolveNodeProvider(ctx, pool)
	if err != nil {
		return err
	}
	if providerNodeID == "" {
		providerNodeID = node.Metadata.Name
	}
	if providerRef == "" {
		providerRef = pool
	}
	if c.Ledger != nil && prov != nil {
		begin, err := c.Ledger.Begin(ctx, providerRef, operations.KindDeleteNode, operations.TargetNode, node.Metadata.Name, map[string]any{
			"providerNodeId": providerNodeID,
		})
		if err != nil {
			return err
		}
		if !begin.SkipProvider {
			callErr := prov.DeleteNode(ctx, begin.Op.ID, providerNodeID)
			if err := c.Ledger.Complete(ctx, begin.Op.ID, nil, callErr); err != nil {
				return err
			}
			if callErr != nil {
				if c.Log != nil {
					c.Log.Warn("DeleteNode failed; will retry",
						"node_id", nodeID,
						"error", callErr.Error(),
					)
				}
				return nil // stay in Deleting
			}
		}
	} else if prov != nil {
		if err := prov.DeleteNode(ctx, "op_drain_"+nodeID, providerNodeID); err != nil {
			return nil
		}
	}

	if c.Timers != nil {
		_ = c.Timers.Clear(ctx, nodeID)
	}
	from := PhaseDeleting
	c.emit(ctx, node, from, "", "DeleteDone")
	if c.Registry != nil {
		_ = c.Registry.Delete(ctx, registryclient.NodePlural, node.Metadata.Name)
	}
	if c.Log != nil {
		c.Log.Info("node deleted",
			"node_id", nodeID,
			"node_pool", pool,
			"from_phase", from,
			"to_phase", "",
			"reason", "DeleteDone",
		)
	}
	return nil
}

func (c *NodeController) checkTimeout(ctx context.Context, node registryclient.Resource, nodeID, pool, phase string) (bool, error) {
	if c.Timers == nil {
		return false, nil
	}
	timer, err := c.Timers.Get(ctx, nodeID)
	if err != nil || timer == nil {
		return false, err
	}
	if timer.TimeoutFired {
		return false, nil
	}
	if c.now().Before(timer.DeadlineAt) {
		return false, nil
	}
	_ = c.Timers.MarkTimeoutFired(ctx, nodeID)
	if c.Log != nil {
		c.Log.Warn("node bootstrap timeout",
			"node_id", nodeID,
			"node_pool", pool,
			"from_phase", phase,
			"reason", TimeoutReasonForPhase(phase),
		)
	}
	if err := c.transition(ctx, node, nodeID, pool, phase, EventTimeout, TimeoutReasonForPhase(phase)); err != nil {
		return true, err
	}
	// Automatic cleanup: Failed → Draining on next observation; do it immediately.
	updated, _ := c.Registry.Get(ctx, registryclient.NodePlural, node.Metadata.Name)
	if updated != nil {
		return true, c.transition(ctx, *updated, nodeID, pool, PhaseFailed, EventFailedCleanup, TimeoutReasonForPhase(phase))
	}
	return true, nil
}

func (c *NodeController) transition(ctx context.Context, node registryclient.Resource, nodeID, pool, from, event, reason string) error {
	tr := NextPhase(from, event)
	if !tr.OK {
		// Failed nodes accept DrainRequested via FailedCleanup path; Ready accepts DrainRequested.
		if from == PhaseReady && event == EventDrainRequested {
			tr = Transition{To: PhaseDraining, Reason: reason, OK: true}
		} else if IsPreReady(from) && event == EventDrainRequested {
			// Scale-down / timeout cleanup of non-ready nodes: go straight to Draining.
			tr = Transition{To: PhaseDraining, Reason: reason, OK: true}
		} else {
			return nil
		}
	}
	if reason == "" {
		reason = tr.Reason
	}
	to := tr.To
	if to == "" {
		return nil
	}

	status := map[string]any{}
	for k, v := range node.Status {
		status[k] = v
	}
	status["phase"] = to
	if reason != "" && (to == PhaseFailed || to == PhaseDraining || to == PhaseDeleting) {
		status["conditions"] = []map[string]any{
			{
				"type":               "Ready",
				"status":             "False",
				"reason":             reason,
				"lastTransitionTime": c.now().Format(time.RFC3339),
			},
		}
		if to == PhaseFailed || reason == ReasonProvisionTimeout || reason == ReasonBootstrapTimeout || reason == ReasonJoinTimeout {
			status["failedReason"] = reason
		}
	}
	if runtimeID, ok := node.Status["runtimeNodeId"]; ok {
		status["runtimeNodeId"] = runtimeID
	}

	if c.Registry != nil {
		if _, err := c.Registry.PutStatus(ctx, registryclient.NodePlural, node.Metadata.Name, node.Metadata.ResourceVersion, status); err != nil {
			return err
		}
	}
	if err := c.ensureTimer(ctx, nodeID, to); err != nil {
		return err
	}
	c.emit(ctx, node, from, to, reason)
	if c.Log != nil {
		c.Log.Info("node phase changed",
			"node_id", nodeID,
			"node_pool", pool,
			"from_phase", from,
			"to_phase", to,
			"reason", reason,
		)
	}
	return nil
}

func (c *NodeController) emit(ctx context.Context, node registryclient.Resource, from, to, reason string) {
	if c.Events == nil {
		return
	}
	resourceID := node.Metadata.ID
	if resourceID == "" {
		resourceID = node.Metadata.Name
	}
	ev := NewPhaseChangedEvent(resourceID, node.Metadata.Generation, from, to, reason, "", c.now())
	_ = c.Events.PublishPhaseChanged(ctx, ev)
}

func (c *NodeController) ensureTimer(ctx context.Context, nodeID, phase string) error {
	if c.Timers == nil || nodeID == "" {
		return nil
	}
	if phase == PhaseReady || phase == PhaseDeleting {
		if phase == PhaseReady {
			return c.Timers.Clear(ctx, nodeID)
		}
		return nil
	}
	existing, err := c.Timers.Get(ctx, nodeID)
	if err != nil {
		return err
	}
	dur := c.timeoutFor(phase)
	if dur <= 0 {
		return nil
	}
	now := c.now()
	if existing != nil && existing.Phase == phase && !existing.DeadlineAt.IsZero() {
		return nil
	}
	started := now
	if existing != nil && existing.Phase == phase && !existing.StartedAt.IsZero() {
		started = existing.StartedAt
	}
	return c.Timers.Upsert(ctx, BootstrapTimer{
		NodeID:     nodeID,
		Phase:      phase,
		StartedAt:  started,
		DeadlineAt: started.Add(dur),
	})
}

func (c *NodeController) timeoutFor(phase string) time.Duration {
	switch phase {
	case PhaseProvisioning:
		if c.Timeouts.Provision > 0 {
			return c.Timeouts.Provision
		}
		return 180 * time.Second
	case PhaseBootstrapping:
		if c.Timeouts.Bootstrap > 0 {
			return c.Timeouts.Bootstrap
		}
		return 600 * time.Second
	case PhaseJoining:
		if c.Timeouts.Join > 0 {
			return c.Timeouts.Join
		}
		return 120 * time.Second
	case PhaseDraining:
		if c.Timeouts.Drain > 0 {
			return c.Timeouts.Drain
		}
		return 300 * time.Second
	default:
		return 0
	}
}

func (c *NodeController) resolveNodeProvider(ctx context.Context, poolName string) (provider.Provider, string, error) {
	if c.ResolveProvider != nil {
		return c.ResolveProvider(ctx, poolName)
	}
	return nil, "", fmt.Errorf("%w: no provider resolver", provider.ErrProviderNotConfigured)
}
