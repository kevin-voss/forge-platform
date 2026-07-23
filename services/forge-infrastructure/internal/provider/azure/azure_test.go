package azure

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"forge.local/services/forge-infrastructure/internal/provider"
)

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(map[string]any{
		"vnetCidr":           "10.40.0.0/16",
		"orphanGraceMinutes": 15,
	}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if err := ValidateConfig(map[string]any{"vnetCidr": "not-a-cidr"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OrphanGraceMinutes != 15 || cfg.VNetCIDR != "10.40.0.0/16" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestCreateNodeIdempotentByOpID(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "westeurope"}, api)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name: "pool-a-0", NodePool: "pool-a", MachineType: "Standard_B2s", Region: "westeurope",
	}
	n1, err := p.CreateNode(ctx, "op_retry1", req)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n1.Labels[TagManaged] != "true" || n1.Labels[TagOpID] != "op_retry1" {
		t.Fatalf("labels: %+v", n1.Labels)
	}
	createsBefore := 0
	for _, c := range api.CallOrder() {
		if c == "vm.create" {
			createsBefore++
		}
	}
	n2, err := p.CreateNode(ctx, "op_retry1", req)
	if err != nil {
		t.Fatalf("CreateNode retry: %v", err)
	}
	if n1.ID != n2.ID {
		t.Fatalf("idempotent CreateNode produced different ids: %s vs %s", n1.ID, n2.ID)
	}
	createsAfter := 0
	for _, c := range api.CallOrder() {
		if c == "vm.create" {
			createsAfter++
		}
	}
	if createsAfter != createsBefore {
		t.Fatalf("expected no second CreateVM, creates before=%d after=%d", createsBefore, createsAfter)
	}
	if api.vmCount() != 1 {
		t.Fatalf("expected 1 vm, got %d", api.vmCount())
	}
}

func TestCreateNodeConcurrentSameOpID(t *testing.T) {
	api := newFakeAPI()
	api.createDelay = 20 * time.Millisecond
	p := NewWithAPI(Config{DefaultRegion: "westeurope"}, api)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name: "pool-b-0", NodePool: "pool-b", MachineType: "Standard_B2s", Region: "westeurope",
	}
	var wg sync.WaitGroup
	results := make([]*provider.ProviderNode, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = p.CreateNode(ctx, "op_concurrent", req)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("CreateNode[%d]: %v", i, err)
		}
	}
	if results[0].ID != results[1].ID {
		t.Fatalf("concurrent CreateNode diverged: %s vs %s", results[0].ID, results[1].ID)
	}
	if api.vmCount() != 1 {
		t.Fatalf("expected exactly 1 vm, got %d", api.vmCount())
	}
}

func TestRateLimitBackoffIncreases(t *testing.T) {
	var sleeps []time.Duration
	lim := NewLimiter(5)
	lim.minBackoff = 100 * time.Millisecond
	lim.maxBackoff = 10 * time.Second
	lim.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}
	ctx := context.Background()
	if err := lim.Backoff429(ctx, http.Header{}); err != nil {
		t.Fatal(err)
	}
	if err := lim.Backoff429(ctx, http.Header{}); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) < 2 || sleeps[1] <= sleeps[0] {
		t.Fatalf("expected increasing backoff, got %v", sleeps)
	}
}

func TestTeardownDeleteOrder(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "westeurope"}, api)
	p.EnableCallRecording()
	ctx := context.Background()

	if _, err := p.CreateNetwork(ctx, "op_net", provider.CreateNetworkRequest{
		Name: "pool-t", CIDR: "10.40.0.0/16", Region: "westeurope",
	}); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	api.mu.Lock()
	for _, n := range api.vnets {
		n.Tags[TagNodePool] = "pool-t"
	}
	api.mu.Unlock()

	node, err := p.CreateNode(ctx, "op_node", provider.CreateNodeRequest{
		Name: "pool-t-0", NodePool: "pool-t", MachineType: "Standard_B2s", Region: "westeurope",
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if _, err := p.AttachDisk(ctx, "op_disk", node.ID, provider.AttachDiskRequest{SizeGiB: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreatePublicIP(ctx, "op_ip", provider.CreatePublicIPRequest{
		Region: "westeurope", NodeID: node.ID,
	}); err != nil {
		t.Fatal(err)
	}

	p.callOrder = nil
	api.mu.Lock()
	api.calls = nil
	api.mu.Unlock()

	if err := p.DeleteNode(ctx, "op_del", node.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	order := api.CallOrder()
	idx := func(name string) int {
		for i, c := range order {
			if c == name {
				return i
			}
		}
		return -1
	}
	dd := idx("disk.detach")
	ddel := idx("disk.delete")
	pd := idx("pip.disassociate")
	pdel := idx("pip.delete")
	vd := idx("vm.delete")
	nd := idx("vnet.delete")
	if dd < 0 || ddel < 0 || pd < 0 || pdel < 0 || vd < 0 || nd < 0 {
		t.Fatalf("missing teardown steps: %v", order)
	}
	if !(dd < ddel && ddel < pd && pd < pdel && pdel < vd && vd < nd) {
		t.Fatalf("teardown order wrong: %v", order)
	}
	if api.vmCount()+api.diskCount()+api.ipCount()+api.vnetCount() != 0 {
		t.Fatalf("leaked resources")
	}
}

func TestReconcileOrphansGracePeriod(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "westeurope", Spec: SpecConfig{OrphanGraceMinutes: 15}}, api)
	ctx := context.Background()

	tracked, err := p.CreateNode(ctx, "op_tracked", provider.CreateNodeRequest{
		Name: "pool-o-0", NodePool: "pool-o", MachineType: "Standard_B2s", Region: "westeurope",
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := p.CreateNode(ctx, "op_old", provider.CreateNodeRequest{
		Name: "pool-o-1", NodePool: "pool-o", MachineType: "Standard_B2s", Region: "westeurope",
	})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, s := range api.vms {
		if nodeIDPrefix+s.ID == old.ID {
			s.Created = time.Now().UTC().Add(-20 * time.Minute)
		}
	}
	api.mu.Unlock()

	fresh, err := p.CreateNode(ctx, "op_fresh", provider.CreateNodeRequest{
		Name: "pool-o-2", NodePool: "pool-o", MachineType: "Standard_B2s", Region: "westeurope",
	})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, s := range api.vms {
		if nodeIDPrefix+s.ID == fresh.ID {
			s.Created = time.Now().UTC().Add(-2 * time.Minute)
		}
	}
	api.mu.Unlock()

	rec := &OrphanReconciler{Provider: p, Known: MapKnown{tracked.ID: {}}, Grace: 15 * time.Minute}
	removed, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed < 1 {
		t.Fatalf("expected orphan deleted, got %d", removed)
	}
	oldID := old.ID[len(nodeIDPrefix):]
	if _, err := api.GetVM(ctx, oldID); err == nil || !IsNotFound(err) {
		t.Fatalf("old orphan should be deleted")
	}
	freshID := fresh.ID[len(nodeIDPrefix):]
	if _, err := api.GetVM(ctx, freshID); err != nil {
		t.Fatalf("fresh should remain: %v", err)
	}
}

func TestLifecycleAllProviderMethods(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "westeurope"}, api)
	ctx := context.Background()

	if err := p.ValidateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListRegions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListMachineTypes(ctx, "westeurope"); err != nil {
		t.Fatal(err)
	}
	net, err := p.CreateNetwork(ctx, "op_net", provider.CreateNetworkRequest{
		Name: "life", CIDR: "10.40.0.0/16", Region: "westeurope",
	})
	if err != nil {
		t.Fatal(err)
	}
	node, err := p.CreateNode(ctx, "op_life", provider.CreateNodeRequest{
		Name: "life-0", NodePool: "life", MachineType: "Standard_B2s", Region: "westeurope",
		UserData: "#cloud-config\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.RebootNode(ctx, "op_reboot", node.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := p.ListNodes(ctx); err != nil || len(list) != 1 {
		t.Fatalf("ListNodes: %v", err)
	}
	disk, err := p.AttachDisk(ctx, "op_vol", node.ID, provider.AttachDiskRequest{SizeGiB: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ResizeDisk(ctx, "op_resize", disk.ID, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreatePublicIP(ctx, "op_fip", provider.CreatePublicIPRequest{Region: "westeurope", NodeID: node.ID}); err != nil {
		t.Fatal(err)
	}
	if pr, err := p.GetPricing(ctx, "westeurope", "Standard_B2s"); err != nil || pr.HourlyUSD <= 0 {
		t.Fatalf("GetPricing: %v", err)
	}
	if err := p.DeleteNode(ctx, "op_del", node.ID); err != nil {
		t.Fatal(err)
	}
	if api.vmCount()+api.diskCount()+api.ipCount() != 0 {
		t.Fatalf("leaks after DeleteNode")
	}
	if err := p.DeleteNetwork(ctx, "op_delnet", net.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRegistersAzure(t *testing.T) {
	reg := provider.NewRegistry(nil)
	reg.Register(provider.TypeAzure, Factory(Config{
		Creds: StaticCredentials{
			TenantID: "t", ClientID: "c", ClientSecret: "s", SubscriptionID: "sub",
		},
		API: newFakeAPI(),
	}))
	if !reg.Has(provider.TypeAzure) {
		t.Fatal("azure not registered")
	}
	p, err := reg.Resolve(provider.TypeAzure, map[string]any{
		"vnetCidr":           "10.9.0.0/16",
		"orphanGraceMinutes": float64(20),
		"tenantId":           "t",
		"clientId":           "c",
		"clientSecret":       "s",
		"subscriptionId":     "sub",
	})
	if err != nil || p == nil {
		t.Fatalf("resolve: %v", err)
	}
}
