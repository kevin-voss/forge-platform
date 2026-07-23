package docker

import (
	"context"
	"errors"
	"testing"

	"forge.local/services/forge-infrastructure/internal/controller"
	"forge.local/services/forge-infrastructure/internal/operations"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/registryclient"
)

func TestLookupMachineTypes(t *testing.T) {
	cases := []struct {
		id        string
		cpu       int
		memoryMiB int
		slots     int
	}{
		{"docker-small", 1, 1024, 2},
		{"docker-medium", 2, 2048, 4},
		{"docker-large", 4, 4096, 8},
	}
	for _, tc := range cases {
		mt, err := LookupMachineType(tc.id)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if mt.CPU != tc.cpu || mt.MemoryMiB != tc.memoryMiB || mt.Slots != tc.slots {
			t.Fatalf("%s: got %+v", tc.id, mt)
		}
	}
	if _, err := LookupMachineType("docker-xlarge"); !errors.Is(err, ErrUnknownMachineType) {
		t.Fatalf("expected ErrUnknownMachineType, got %v", err)
	}
}

func TestManagedLabels(t *testing.T) {
	labels := ManagedLabels("pool-a", "op_abc")
	if labels[LabelManaged] != LabelManagedValue {
		t.Fatalf("managed=%q", labels[LabelManaged])
	}
	if labels[LabelPool] != "pool-a" || labels[LabelNodePool] != "pool-a" {
		t.Fatalf("pool labels: %+v", labels)
	}
	if labels[LabelOpID] != "op_abc" {
		t.Fatalf("op_id=%q", labels[LabelOpID])
	}
}

func TestCreateNodeAppliesLabelsAndIdempotentByOpID(t *testing.T) {
	eng := newFakeEngine()
	p := NewWithEngine(Config{Network: "forge-platform_default", Image: "forge/forge-runtime:local"}, eng)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name:        "smoke-pool-0",
		NodePool:    "smoke-pool",
		MachineType: "docker-small",
		Region:      "local",
		Labels:      map[string]string{"forge.pool": "smoke-pool"},
	}
	n1, err := p.CreateNode(ctx, "op_retry1", req)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n1.ID == "" || n1.Address == "" || n1.MachineType != "docker-small" {
		t.Fatalf("node: %+v", n1)
	}
	for _, key := range []string{LabelManaged, LabelPool, LabelOpID, LabelNodePool} {
		if n1.Labels[key] == "" {
			t.Fatalf("missing label %s on create: %+v", key, n1.Labels)
		}
	}
	if n1.Labels[LabelManaged] != "true" || n1.Labels[LabelOpID] != "op_retry1" {
		t.Fatalf("labels: %+v", n1.Labels)
	}
	n2, err := p.CreateNode(ctx, "op_retry1", req)
	if err != nil {
		t.Fatalf("CreateNode retry: %v", err)
	}
	if n1.ID != n2.ID {
		t.Fatalf("idempotent CreateNode produced different ids: %s vs %s", n1.ID, n2.ID)
	}
	if eng.containerCount() != 1 {
		t.Fatalf("expected 1 container, got %d", eng.containerCount())
	}
}

func TestDeleteNodeRemovesContainerAndVolume(t *testing.T) {
	eng := newFakeEngine()
	p := NewWithEngine(Config{}, eng)
	ctx := context.Background()
	n, err := p.CreateNode(ctx, "op_del", provider.CreateNodeRequest{
		NodePool: "p", MachineType: "docker-small", Name: "p-0",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if eng.volumeCount() != 1 || eng.containerCount() != 1 {
		t.Fatalf("pre-delete vols=%d containers=%d", eng.volumeCount(), eng.containerCount())
	}
	if err := p.DeleteNode(ctx, "op_del2", n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if eng.containerCount() != 0 || eng.volumeCount() != 0 {
		t.Fatalf("post-delete vols=%d containers=%d", eng.volumeCount(), eng.containerCount())
	}
}

func TestRebootGetListPricingPublicIP(t *testing.T) {
	eng := newFakeEngine()
	p := NewWithEngine(Config{HostAddress: "127.0.0.1"}, eng)
	ctx := context.Background()
	n, err := p.CreateNode(ctx, "op_r", provider.CreateNodeRequest{
		NodePool: "p", MachineType: "docker-medium", Name: "p-0",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := p.RebootNode(ctx, "op_rb", n.ID); err != nil {
		t.Fatalf("reboot: %v", err)
	}
	got, err := p.GetNode(ctx, n.ID)
	if err != nil || got == nil || got.Phase != "Ready" {
		t.Fatalf("GetNode: %+v err=%v", got, err)
	}
	list, err := p.ListNodes(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListNodes: %v len=%d", err, len(list))
	}
	price, err := p.GetPricing(ctx, "local", "docker-small")
	if err != nil || price.HourlyUSD != 0 || price.Currency != "USD" {
		t.Fatalf("pricing: %+v err=%v", price, err)
	}
	ip, err := p.CreatePublicIP(ctx, "op_ip", provider.CreatePublicIPRequest{Name: "x"})
	if err != nil || ip.Address != "127.0.0.1" {
		t.Fatalf("public ip: %+v err=%v", ip, err)
	}
	if err := p.DeletePublicIP(ctx, "op_ip2", ip.ID); err != nil {
		t.Fatalf("delete public ip: %v", err)
	}
	if err := p.ValidateCredentials(ctx); err != nil {
		t.Fatalf("validate: %v", err)
	}
	regions, err := p.ListRegions(ctx)
	if err != nil || len(regions) != 1 || regions[0].ID != "local" {
		t.Fatalf("regions: %+v err=%v", regions, err)
	}
	mts, err := p.ListMachineTypes(ctx, "local")
	if err != nil || len(mts) != 3 {
		t.Fatalf("machine types: %v len=%d", err, len(mts))
	}
}

func TestNetworkAndDisk(t *testing.T) {
	eng := newFakeEngine()
	p := NewWithEngine(Config{Network: "forge-platform_default"}, eng)
	ctx := context.Background()

	// Default network reuse
	net, err := p.CreateNetwork(ctx, "op_n1", provider.CreateNetworkRequest{Name: "forge-platform_default"})
	if err != nil || net == nil {
		t.Fatalf("create default net: %v %+v", err, net)
	}
	if err := p.DeleteNetwork(ctx, "op_n1d", net.ID); err != nil {
		t.Fatalf("delete default net: %v", err)
	}

	isolated, err := p.CreateNetwork(ctx, "op_n2", provider.CreateNetworkRequest{Name: "pool-isolated"})
	if err != nil {
		t.Fatalf("create isolated: %v", err)
	}
	if err := p.DeleteNetwork(ctx, "op_n2d", isolated.ID); err != nil {
		t.Fatalf("delete isolated: %v", err)
	}

	disk, err := p.AttachDisk(ctx, "op_d", "docker:abc", provider.AttachDiskRequest{SizeGiB: 10, Name: "d1"})
	if err != nil || disk.SizeGiB != 10 {
		t.Fatalf("attach: %+v err=%v", disk, err)
	}
	if err := p.ResizeDisk(ctx, "op_r", disk.ID, 20); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := p.DetachDisk(ctx, "op_dd", "docker:abc", disk.ID); err != nil {
		t.Fatalf("detach: %v", err)
	}
}

func TestCreateNodeCleansUpOnStartFailure(t *testing.T) {
	eng := newFakeEngine()
	eng.failStart = true
	p := NewWithEngine(Config{}, eng)
	_, err := p.CreateNode(context.Background(), "op_fail", provider.CreateNodeRequest{
		NodePool: "p", MachineType: "docker-small", Name: "p-0",
	})
	if err == nil {
		t.Fatal("expected start failure")
	}
	if eng.containerCount() != 0 || eng.volumeCount() != 0 {
		t.Fatalf("expected cleanup, vols=%d containers=%d", eng.volumeCount(), eng.containerCount())
	}
}

func TestReconcileOrphans(t *testing.T) {
	eng := newFakeEngine()
	p := NewWithEngine(Config{}, eng)
	ctx := context.Background()

	kept, err := p.CreateNode(ctx, "op_keep", provider.CreateNodeRequest{
		NodePool: "p", MachineType: "docker-small", Name: "p-0",
	})
	if err != nil {
		t.Fatalf("create kept: %v", err)
	}
	orphan, err := p.CreateNode(ctx, "op_orphan", provider.CreateNodeRequest{
		NodePool: "p", MachineType: "docker-small", Name: "p-1",
	})
	if err != nil {
		t.Fatalf("create orphan: %v", err)
	}

	rec := &OrphanReconciler{
		Provider: p,
		Known:    &memKnown{ids: map[string]struct{}{kept.ID: {}}},
	}
	n, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed=%d want 1", n)
	}
	if eng.containerCount() != 1 {
		t.Fatalf("containers=%d want 1", eng.containerCount())
	}
	if _, err := p.GetNode(ctx, kept.ID); err != nil {
		t.Fatalf("kept node missing: %v", err)
	}
	if _, err := p.GetNode(ctx, orphan.ID); err == nil {
		t.Fatal("orphan still present")
	}
	if p.OrphansRemoved() != 1 {
		t.Fatalf("orphans metric=%d", p.OrphansRemoved())
	}
}

func TestProviderInterfaceConformance(t *testing.T) {
	var _ provider.Provider = (*Provider)(nil)
}

func TestNodePoolControllerConvergesWithDockerProvider(t *testing.T) {
	eng := newFakeEngine()
	dockerProv := NewWithEngine(Config{}, eng)

	reg := provider.NewRegistry(nil)
	reg.Register(provider.TypeDocker, func(cfg map[string]any) (provider.Provider, error) {
		return dockerProv, nil
	})

	fr := &fakeRegistry{
		providers: map[string]registryclient.Resource{
			"docker-local": {
				Metadata: registryclient.Metadata{Name: "docker-local"},
				Spec:     map[string]any{"type": "docker"},
			},
		},
		pools: map[string]registryclient.Resource{
			"smoke-pool": {
				Metadata: registryclient.Metadata{Name: "smoke-pool", Generation: 1, ResourceVersion: "1"},
				Spec: map[string]any{
					"providerRef": "docker-local",
					"replicas":    3,
					"machineType": "docker-small",
					"region":      "local",
				},
			},
		},
	}
	ledger := newMemLedger()
	ctrl := &controller.NodePoolController{
		Registry:  fr,
		Ledger:    ledger,
		Providers: reg,
		Interval:  0,
	}

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := ctrl.Reconcile(ctx, fr.pools["smoke-pool"]); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		// refresh pool resourceVersion from last status write
		if p, ok := fr.pools["smoke-pool"]; ok {
			fr.pools["smoke-pool"] = p
		}
	}
	if eng.containerCount() != 3 {
		t.Fatalf("containers=%d want 3", eng.containerCount())
	}
	if len(fr.nodes) != 3 {
		t.Fatalf("nodes=%d want 3", len(fr.nodes))
	}
	list, err := dockerProv.ListNodes(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("ListNodes: %v len=%d", err, len(list))
	}
	for _, n := range list {
		if n.Labels[LabelManaged] != "true" || n.Labels[LabelPool] != "smoke-pool" {
			t.Fatalf("bad labels: %+v", n.Labels)
		}
	}
}

// --- minimal fakes mirrored from controller_test (kept local to avoid export) ---

type memLedger struct {
	rows map[string]*operations.Operation
	ids  *operations.Generator
}

func newMemLedger() *memLedger {
	return &memLedger{rows: map[string]*operations.Operation{}, ids: operations.NewGenerator()}
}

func (m *memLedger) Begin(ctx context.Context, providerName, kind, targetKind, naturalKey string, request any) (*operations.BeginResult, error) {
	k := providerName + "|" + naturalKey
	if existing, ok := m.rows[k]; ok {
		skip := existing.Status == operations.StatusPending || existing.Status == operations.StatusSucceeded
		return &operations.BeginResult{Op: existing, AlreadyExists: true, SkipProvider: skip}, nil
	}
	op := &operations.Operation{
		ID: m.ids.NewOpID(), ProviderName: providerName, Kind: kind, TargetKind: targetKind,
		NaturalKey: naturalKey, Status: operations.StatusPending,
	}
	m.rows[k] = op
	return &operations.BeginResult{Op: op}, nil
}

func (m *memLedger) Complete(ctx context.Context, opID string, result any, callErr error) error {
	for _, op := range m.rows {
		if op.ID == opID {
			if callErr != nil {
				op.Status = operations.StatusFailed
			} else {
				op.Status = operations.StatusSucceeded
			}
			return nil
		}
	}
	return nil
}

type fakeRegistry struct {
	providers map[string]registryclient.Resource
	pools     map[string]registryclient.Resource
	nodes     []registryclient.Resource
}

func (f *fakeRegistry) List(ctx context.Context, plural, labelSelector string) ([]registryclient.Resource, error) {
	switch plural {
	case "nodepools":
		out := make([]registryclient.Resource, 0, len(f.pools))
		for _, p := range f.pools {
			out = append(out, p)
		}
		return out, nil
	case "nodes":
		return append([]registryclient.Resource{}, f.nodes...), nil
	case "infrastructureproviders":
		out := make([]registryclient.Resource, 0, len(f.providers))
		for _, p := range f.providers {
			out = append(out, p)
		}
		return out, nil
	}
	return nil, nil
}

func (f *fakeRegistry) Get(ctx context.Context, plural, name string) (*registryclient.Resource, error) {
	switch plural {
	case "infrastructureproviders":
		if p, ok := f.providers[name]; ok {
			cp := p
			return &cp, nil
		}
	case "nodepools":
		if p, ok := f.pools[name]; ok {
			cp := p
			return &cp, nil
		}
	case "nodes":
		for i := range f.nodes {
			if f.nodes[i].Metadata.Name == name {
				cp := f.nodes[i]
				return &cp, nil
			}
		}
	}
	return nil, nil
}

func (f *fakeRegistry) PutStatus(ctx context.Context, plural, name, resourceVersion string, status map[string]any) (*registryclient.Resource, error) {
	if plural == "nodepools" {
		p := f.pools[name]
		p.Status = status
		p.Metadata.ResourceVersion = resourceVersion + "x"
		f.pools[name] = p
		return &p, nil
	}
	return nil, nil
}

func (f *fakeRegistry) Create(ctx context.Context, plural string, res registryclient.Resource) (*registryclient.Resource, error) {
	if plural == "nodes" {
		if res.Status == nil {
			res.Status = map[string]any{}
		}
		if _, ok := res.Status["phase"]; !ok {
			res.Status["phase"] = "Provisioning"
		}
		f.nodes = append(f.nodes, res)
		return &res, nil
	}
	return &res, nil
}

func (f *fakeRegistry) Delete(ctx context.Context, plural, name string) error {
	if plural != "nodes" {
		return nil
	}
	out := f.nodes[:0]
	for _, n := range f.nodes {
		if n.Metadata.Name != name {
			out = append(out, n)
		}
	}
	f.nodes = out
	return nil
}
