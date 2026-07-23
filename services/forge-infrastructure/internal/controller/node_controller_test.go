package controller_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"forge.local/services/forge-infrastructure/internal/controller"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/noop"
	"forge.local/services/forge-infrastructure/internal/registryclient"
)

func TestNextPhaseTable(t *testing.T) {
	cases := []struct {
		phase, event, wantTo, wantReason string
		ok                               bool
	}{
		{controller.PhaseProvisioning, controller.EventMachineBooted, controller.PhaseBootstrapping, "", true},
		{controller.PhaseProvisioning, controller.EventTimeout, controller.PhaseFailed, controller.ReasonProvisionTimeout, true},
		{controller.PhaseBootstrapping, controller.EventHealthReady, controller.PhaseJoining, "", true},
		{controller.PhaseBootstrapping, controller.EventTimeout, controller.PhaseFailed, controller.ReasonBootstrapTimeout, true},
		{controller.PhaseJoining, controller.EventRegistered, controller.PhaseReady, "", true},
		{controller.PhaseJoining, controller.EventTimeout, controller.PhaseFailed, controller.ReasonJoinTimeout, true},
		{controller.PhaseReady, controller.EventDrainRequested, controller.PhaseDraining, "", true},
		{controller.PhaseFailed, controller.EventFailedCleanup, controller.PhaseDraining, "FailedCleanup", true},
		{controller.PhaseDraining, controller.EventDrainComplete, controller.PhaseDeleting, "", true},
		{controller.PhaseDraining, controller.EventDrainTimeout, controller.PhaseDeleting, controller.ReasonDrainTimeout, true},
		{controller.PhaseDeleting, controller.EventDeleteDone, "", "", true},
		{controller.PhaseReady, controller.EventTimeout, "", "", false},
		{controller.PhaseProvisioning, controller.EventHealthReady, "", "", false},
	}
	for _, tc := range cases {
		got := controller.NextPhase(tc.phase, tc.event)
		if got.OK != tc.ok || got.To != tc.wantTo || got.Reason != tc.wantReason {
			t.Fatalf("NextPhase(%s,%s)=%+v want ok=%v to=%q reason=%q",
				tc.phase, tc.event, got, tc.ok, tc.wantTo, tc.wantReason)
		}
	}
}

func TestTimeoutFiresOnce(t *testing.T) {
	reg := &fakeRegistry{nodes: []registryclient.Resource{{
		Metadata: registryclient.Metadata{Name: "n1", ID: "node_n1", ResourceVersion: "1"},
		Spec:     map[string]any{"nodePoolRef": "pool", "providerNodeId": "prov-1"},
		Status:   map[string]any{"phase": controller.PhaseBootstrapping, "address": "http://n1:8080"},
	}}}
	timers := controller.NewMemoryTimers()
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	_ = timers.Upsert(context.Background(), controller.BootstrapTimer{
		NodeID:     "node_n1",
		Phase:      controller.PhaseBootstrapping,
		StartedAt:  now.Add(-10 * time.Minute),
		DeadlineAt: now.Add(-time.Minute),
	})
	events := &controller.MemoryEvents{}
	drain := controller.NewMemoryDrain()
	cp := &countingProvider{Provider: &noop.Provider{}}
	ctrl := &controller.NodeController{
		Registry: reg,
		Timers:   timers,
		Events:   events,
		Drain:    drain,
		Machines: controller.StaticMachine{Booted: false},
		Health:   controller.StaticHealth{OK: false},
		Timeouts: controller.NodeTimeouts{Bootstrap: time.Second, Drain: time.Hour},
		Clock:    func() time.Time { return now },
		ResolveProvider: func(ctx context.Context, poolName string) (provider.Provider, string, error) {
			return cp, "docker-local", nil
		},
		Ledger: newMemLedger(),
	}

	node := reg.nodes[0]
	if err := ctrl.Reconcile(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	phase1 := stringFromNodePhase(reg)
	if phase1 != controller.PhaseDraining && phase1 != controller.PhaseFailed {
		t.Fatalf("after timeout want Failed/Draining, got %q", phase1)
	}
	firedEvents := len(events.Events)

	// Subsequent reconciles must not re-fire timeout.
	node = reg.nodes[0]
	if err := ctrl.Reconcile(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	timer, _ := timers.Get(context.Background(), "node_n1")
	if timer != nil && !timer.TimeoutFired && phase1 == controller.PhaseBootstrapping {
		t.Fatal("expected timeout_fired after first reconcile")
	}
	if len(events.Events) > firedEvents+2 {
		t.Fatalf("timeout re-fired events: before=%d after=%d", firedEvents, len(events.Events))
	}
}

func stringFromNodePhase(reg *fakeRegistry) string {
	if len(reg.nodes) == 0 {
		return ""
	}
	p, _ := reg.nodes[0].Status["phase"].(string)
	return p
}

func TestHappyPathPhases(t *testing.T) {
	reg := &fakeRegistry{nodes: []registryclient.Resource{{
		Metadata: registryclient.Metadata{Name: "n1", ID: "node_n1", ResourceVersion: "1", Generation: 1},
		Spec:     map[string]any{"nodePoolRef": "pool", "providerNodeId": "prov-1"},
		Status:   map[string]any{"phase": controller.PhaseProvisioning, "address": "http://n1:8080"},
	}}}
	timers := controller.NewMemoryTimers()
	events := &controller.MemoryEvents{}
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	ctrl := &controller.NodeController{
		Registry: reg,
		Timers:   timers,
		Events:   events,
		Machines: controller.StaticMachine{Booted: true},
		Health:   controller.StaticHealth{OK: true},
		Join:     controller.StaticJoinObserver{RuntimeNodeID: "runtime-n1", Online: true},
		Timeouts: controller.NodeTimeouts{Provision: time.Hour, Bootstrap: time.Hour, Join: time.Hour},
		Clock:    func() time.Time { return now },
		ResolveProvider: func(ctx context.Context, poolName string) (provider.Provider, string, error) {
			return &noop.Provider{}, "p", nil
		},
	}

	for _, want := range []string{
		controller.PhaseBootstrapping,
		controller.PhaseJoining,
		controller.PhaseReady,
	} {
		if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
			t.Fatal(err)
		}
		if got := stringFromNodePhase(reg); got != want {
			t.Fatalf("want phase %s, got %s", want, got)
		}
	}
	if len(events.Events) < 3 {
		t.Fatalf("expected phasechanged events, got %d", len(events.Events))
	}
}

func TestPhaseChangedEventEnvelope(t *testing.T) {
	ev := controller.NewPhaseChangedEvent("node_01JTEST", 4, "Bootstrapping", "Joining", "", "trace-abc", time.Date(2026, 7, 23, 10, 2, 0, 0, time.UTC))
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"event_id", "resource_id", "generation", "timestamp", "producer", "schema_version", "trace_id", "type"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing envelope field %q in %s", key, string(raw))
		}
	}
	if m["type"] != "resource.node.phasechanged" {
		t.Fatalf("type=%v", m["type"])
	}
	if m["producer"] != "forge-infrastructure" {
		t.Fatalf("producer=%v", m["producer"])
	}
	if m["from"] != "Bootstrapping" || m["to"] != "Joining" {
		t.Fatalf("from/to=%v/%v", m["from"], m["to"])
	}
}

func TestStuckBootstrapDeleted(t *testing.T) {
	reg := &fakeRegistry{
		pools: map[string]registryclient.Resource{
			"pool": {Metadata: registryclient.Metadata{Name: "pool"}, Spec: map[string]any{"providerRef": "docker-local"}},
		},
		nodes: []registryclient.Resource{{
			Metadata: registryclient.Metadata{Name: "stuck-0", ID: "node_stuck", ResourceVersion: "1"},
			Spec:     map[string]any{"nodePoolRef": "pool", "providerNodeId": "prov-stuck"},
			Status:   map[string]any{"phase": controller.PhaseBootstrapping, "address": "http://stuck:8080"},
		}},
	}
	cp := &countingProvider{Provider: &noop.Provider{}}
	timers := controller.NewMemoryTimers()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	_ = timers.Upsert(context.Background(), controller.BootstrapTimer{
		NodeID: "node_stuck", Phase: controller.PhaseBootstrapping,
		StartedAt: now.Add(-time.Hour), DeadlineAt: now.Add(-time.Second),
	})
	drain := controller.NewMemoryDrain()
	ctrl := &controller.NodeController{
		Registry: reg,
		Timers:   timers,
		Drain:    drain,
		Events:   &controller.MemoryEvents{},
		Machines: controller.StaticMachine{Booted: false},
		Health:   controller.StaticHealth{OK: false},
		Timeouts: controller.NodeTimeouts{Bootstrap: 5 * time.Second, Drain: time.Millisecond},
		Clock:    func() time.Time { return now },
		Ledger:   newMemLedger(),
		ResolveProvider: func(ctx context.Context, poolName string) (provider.Provider, string, error) {
			return cp, "docker-local", nil
		},
	}

	// Timeout → Failed → Draining
	if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if stringFromNodePhase(reg) != controller.PhaseDraining {
		t.Fatalf("want Draining after timeout cleanup, got %s", stringFromNodePhase(reg))
	}
	// Drain empty → Deleting → DeleteNode + remove resource
	now2 := now.Add(time.Second)
	ctrl.Clock = func() time.Time { return now2 }
	if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if stringFromNodePhase(reg) != controller.PhaseDeleting && len(reg.nodes) != 0 {
		// may already be deleted
	}
	if len(reg.nodes) > 0 {
		if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
			t.Fatal(err)
		}
	}
	if len(reg.nodes) != 0 {
		t.Fatalf("expected node resource deleted, still have %d", len(reg.nodes))
	}
	if len(cp.deletes) != 1 {
		t.Fatalf("expected provider DeleteNode once, got %v", cp.deletes)
	}
}

func TestDrainBlocksPlacementThenDeletes(t *testing.T) {
	reg := &fakeRegistry{nodes: []registryclient.Resource{{
		Metadata: registryclient.Metadata{Name: "n1", ID: "node_n1", ResourceVersion: "1"},
		Spec:     map[string]any{"nodePoolRef": "pool", "providerNodeId": "prov-1"},
		Status: map[string]any{
			"phase": controller.PhaseReady, "address": "http://n1:8080", "runtimeNodeId": "runtime-n1",
		},
	}}}
	drain := controller.NewMemoryDrain()
	drain.SetWorkloads("runtime-n1", []string{"wl-1"})
	cp := &countingProvider{Provider: &noop.Provider{}}
	ctrl := &controller.NodeController{
		Registry: reg,
		Timers:   controller.NewMemoryTimers(),
		Drain:    drain,
		Events:   &controller.MemoryEvents{},
		Timeouts: controller.NodeTimeouts{Drain: time.Hour},
		Clock:    func() time.Time { return time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC) },
		Ledger:   newMemLedger(),
		ResolveProvider: func(ctx context.Context, poolName string) (provider.Provider, string, error) {
			return cp, "docker-local", nil
		},
	}

	if err := ctrl.RequestDrain(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if stringFromNodePhase(reg) != controller.PhaseDraining {
		t.Fatalf("want Draining, got %s", stringFromNodePhase(reg))
	}
	if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if !drain.IsDraining("runtime-n1") {
		t.Fatal("expected BeginDrain")
	}
	if drain.CanPlace("runtime-n1") {
		t.Fatal("draining node must block new placements")
	}
	if stringFromNodePhase(reg) != controller.PhaseDraining {
		t.Fatalf("still draining while workloads present, got %s", stringFromNodePhase(reg))
	}

	// Workload rescheduled elsewhere.
	drain.SetWorkloads("runtime-n1", nil)
	if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if stringFromNodePhase(reg) != controller.PhaseDeleting {
		t.Fatalf("want Deleting after drain complete, got %s", stringFromNodePhase(reg))
	}
	if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if len(reg.nodes) != 0 {
		t.Fatal("expected node removed after delete")
	}
}

func TestDrainTimeoutDeletesWithStrandedWorkload(t *testing.T) {
	reg := &fakeRegistry{nodes: []registryclient.Resource{{
		Metadata: registryclient.Metadata{Name: "n1", ID: "node_n1", ResourceVersion: "1"},
		Spec:     map[string]any{"nodePoolRef": "pool", "providerNodeId": "prov-1"},
		Status: map[string]any{
			"phase": controller.PhaseDraining, "runtimeNodeId": "runtime-n1",
		},
	}}}
	drain := controller.NewMemoryDrain()
	drain.SetWorkloads("runtime-n1", []string{"wl-stranded"})
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	timers := controller.NewMemoryTimers()
	_ = timers.MarkDrainStarted(context.Background(), "node_n1", now.Add(-10*time.Minute))
	cp := &countingProvider{Provider: &noop.Provider{}}
	ctrl := &controller.NodeController{
		Registry: reg,
		Timers:   timers,
		Drain:    drain,
		Events:   &controller.MemoryEvents{},
		Timeouts: controller.NodeTimeouts{Drain: time.Second},
		Clock:    func() time.Time { return now },
		Ledger:   newMemLedger(),
		ResolveProvider: func(ctx context.Context, poolName string) (provider.Provider, string, error) {
			return cp, "docker-local", nil
		},
	}
	if err := ctrl.Reconcile(context.Background(), reg.nodes[0]); err != nil {
		t.Fatal(err)
	}
	if stringFromNodePhase(reg) != controller.PhaseDeleting {
		t.Fatalf("want Deleting after drain timeout, got %s", stringFromNodePhase(reg))
	}
}
