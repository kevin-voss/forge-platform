package aws

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
		"vpcCidr":            "10.30.0.0/16",
		"orphanGraceMinutes": 15,
	}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if err := ValidateConfig(map[string]any{"vpcCidr": "not-a-cidr"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
	if err := ValidateConfig(map[string]any{"orphanGraceMinutes": 0}); err == nil {
		t.Fatal("expected orphanGraceMinutes error")
	}
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OrphanGraceMinutes != 15 || cfg.VPCCIDR != "10.30.0.0/16" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestCreateNodeIdempotentByOpID(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "eu-central-1", Spec: SpecConfig{AMI: "ami-test"}}, api)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name: "pool-a-0", NodePool: "pool-a", MachineType: "t3.medium", Region: "eu-central-1",
	}
	n1, err := p.CreateNode(ctx, "op_retry1", req)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n1.ID == "" || n1.Address == "" || n1.MachineType != "t3.medium" {
		t.Fatalf("node: %+v", n1)
	}
	if n1.Labels[TagManaged] != "true" || n1.Labels[TagOpID] != "op_retry1" {
		t.Fatalf("labels: %+v", n1.Labels)
	}
	createsBefore := 0
	for _, c := range api.CallOrder() {
		if c == "instance.create" {
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
		if c == "instance.create" {
			createsAfter++
		}
	}
	if createsAfter != createsBefore {
		t.Fatalf("expected no second RunInstances, creates before=%d after=%d calls=%v", createsBefore, createsAfter, api.CallOrder())
	}
	if api.instanceCount() != 1 {
		t.Fatalf("expected 1 instance, got %d", api.instanceCount())
	}
}

func TestCreateNodeConcurrentSameOpID(t *testing.T) {
	api := newFakeAPI()
	api.createDelay = 20 * time.Millisecond
	p := NewWithAPI(Config{DefaultRegion: "eu-central-1"}, api)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name: "pool-b-0", NodePool: "pool-b", MachineType: "t3.medium", Region: "eu-central-1",
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
	if api.instanceCount() != 1 {
		t.Fatalf("expected exactly 1 instance, got %d", api.instanceCount())
	}
}

func TestRateLimitBackoffIncreases(t *testing.T) {
	var sleeps []time.Duration
	lim := NewLimiter(5)
	lim.minBackoff = 100 * time.Millisecond
	lim.maxBackoff = 10 * time.Second
	base := time.Unix(1_700_000_000, 0)
	lim.now = func() time.Time { return base }
	lim.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}
	ctx := context.Background()
	h1 := http.Header{}
	if err := lim.Backoff429(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if err := lim.Backoff429(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) < 2 || sleeps[1] <= sleeps[0] {
		t.Fatalf("expected increasing backoff, got %v", sleeps)
	}
	sleeps = nil
	lim.consecutive = 0
	h2 := http.Header{}
	h2.Set("Retry-After", "5")
	if err := lim.Backoff429(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] < 4*time.Second {
		t.Fatalf("expected delay respecting Retry-After (~5s), got %v", sleeps)
	}
}

func TestTeardownDeleteOrder(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "eu-central-1"}, api)
	p.EnableCallRecording()
	ctx := context.Background()

	net, err := p.CreateNetwork(ctx, "op_net", provider.CreateNetworkRequest{
		Name: "pool-t", CIDR: "10.30.0.0/16", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	api.mu.Lock()
	for _, n := range api.vpcs {
		n.Tags[TagNodePool] = "pool-t"
	}
	api.mu.Unlock()
	_ = net

	node, err := p.CreateNode(ctx, "op_node", provider.CreateNodeRequest{
		Name: "pool-t-0", NodePool: "pool-t", MachineType: "t3.medium", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if _, err := p.AttachDisk(ctx, "op_disk", node.ID, provider.AttachDiskRequest{SizeGiB: 20, Name: "data"}); err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}
	if _, err := p.CreatePublicIP(ctx, "op_ip", provider.CreatePublicIPRequest{
		Region: "eu-central-1", NodeID: node.ID, Name: "stable",
	}); err != nil {
		t.Fatalf("CreatePublicIP: %v", err)
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
	vd := idx("volume.detach")
	vdel := idx("volume.delete")
	ed := idx("eip.disassociate")
	er := idx("eip.release")
	id := idx("instance.delete")
	nd := idx("vpc.delete")
	if vd < 0 || vdel < 0 || ed < 0 || er < 0 || id < 0 || nd < 0 {
		t.Fatalf("missing teardown steps in call order: %v", order)
	}
	if !(vd < vdel && vdel < ed && ed < er && er < id && id < nd) {
		t.Fatalf("teardown order wrong: %v", order)
	}
	if api.instanceCount() != 0 || api.volumeCount() != 0 || api.eipCount() != 0 || api.vpcCount() != 0 {
		t.Fatalf("leaked resources: instances=%d vols=%d eips=%d vpcs=%d",
			api.instanceCount(), api.volumeCount(), api.eipCount(), api.vpcCount())
	}
}

func TestReconcileOrphansGracePeriod(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{
		DefaultRegion: "eu-central-1",
		Spec:          SpecConfig{OrphanGraceMinutes: 15},
	}, api)
	ctx := context.Background()

	tracked, err := p.CreateNode(ctx, "op_tracked", provider.CreateNodeRequest{
		Name: "pool-o-0", NodePool: "pool-o", MachineType: "t3.medium", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := p.CreateNode(ctx, "op_old", provider.CreateNodeRequest{
		Name: "pool-o-1", NodePool: "pool-o", MachineType: "t3.medium", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, s := range api.instances {
		if nodeIDPrefix+s.ID == old.ID {
			s.Created = time.Now().UTC().Add(-20 * time.Minute)
		}
	}
	api.mu.Unlock()

	fresh, err := p.CreateNode(ctx, "op_fresh", provider.CreateNodeRequest{
		Name: "pool-o-2", NodePool: "pool-o", MachineType: "t3.medium", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, s := range api.instances {
		if nodeIDPrefix+s.ID == fresh.ID {
			s.Created = time.Now().UTC().Add(-2 * time.Minute)
		}
	}
	api.mu.Unlock()

	known := MapKnown{tracked.ID: {}}
	rec := &OrphanReconciler{Provider: p, Known: known, Grace: 15 * time.Minute}
	removed, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if removed < 1 {
		t.Fatalf("expected at least 1 orphan deleted, got %d", removed)
	}
	oldID := old.ID[len(nodeIDPrefix):]
	if _, err := api.GetInstance(ctx, "eu-central-1", oldID); err == nil || !IsNotFound(err) {
		t.Fatalf("old orphan should be deleted, err=%v", err)
	}
	freshID := fresh.ID[len(nodeIDPrefix):]
	if _, err := api.GetInstance(ctx, "eu-central-1", freshID); err != nil {
		t.Fatalf("fresh orphan within grace should remain: %v", err)
	}
	trackedID := tracked.ID[len(nodeIDPrefix):]
	if _, err := api.GetInstance(ctx, "eu-central-1", trackedID); err != nil {
		t.Fatalf("tracked node should remain: %v", err)
	}
}

func TestLifecycleAllProviderMethods(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "eu-central-1"}, api)
	ctx := context.Background()

	if err := p.ValidateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	regs, err := p.ListRegions(ctx)
	if err != nil || len(regs) == 0 {
		t.Fatalf("ListRegions: %v", err)
	}
	mts, err := p.ListMachineTypes(ctx, "eu-central-1")
	if err != nil || len(mts) == 0 {
		t.Fatalf("ListMachineTypes: %v", err)
	}
	net, err := p.CreateNetwork(ctx, "op_net", provider.CreateNetworkRequest{
		Name: "life", CIDR: "10.30.0.0/16", Region: "eu-central-1",
	})
	if err != nil || net.ID == "" {
		t.Fatalf("CreateNetwork: %+v err=%v", net, err)
	}
	node, err := p.CreateNode(ctx, "op_life", provider.CreateNodeRequest{
		Name: "life-0", NodePool: "life", MachineType: "t3.medium", Region: "eu-central-1",
		UserData: "#cloud-config\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if node.Address == "" {
		t.Fatal("expected public IP address")
	}
	got, err := p.GetNode(ctx, node.ID)
	if err != nil || got.ID != node.ID {
		t.Fatalf("GetNode: %+v err=%v", got, err)
	}
	if err := p.RebootNode(ctx, "op_reboot", node.ID); err != nil {
		t.Fatal(err)
	}
	list, err := p.ListNodes(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListNodes: %v len=%d", err, len(list))
	}
	disk, err := p.AttachDisk(ctx, "op_vol", node.ID, provider.AttachDiskRequest{SizeGiB: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ResizeDisk(ctx, "op_resize", disk.ID, 20); err != nil {
		t.Fatal(err)
	}
	ip, err := p.CreatePublicIP(ctx, "op_fip", provider.CreatePublicIPRequest{Region: "eu-central-1", NodeID: node.ID})
	if err != nil || ip.Address == "" {
		t.Fatalf("CreatePublicIP: %+v err=%v", ip, err)
	}
	pr, err := p.GetPricing(ctx, "eu-central-1", "t3.medium")
	if err != nil || pr.HourlyUSD <= 0 {
		t.Fatalf("GetPricing: %+v err=%v", pr, err)
	}
	if err := p.DeleteNode(ctx, "op_del", node.ID); err != nil {
		t.Fatal(err)
	}
	if api.instanceCount()+api.volumeCount()+api.eipCount() != 0 {
		t.Fatalf("leaks after DeleteNode: instances=%d vols=%d eips=%d",
			api.instanceCount(), api.volumeCount(), api.eipCount())
	}
	if err := p.DeleteNetwork(ctx, "op_delnet", net.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRegistersAWS(t *testing.T) {
	reg := provider.NewRegistry(nil)
	reg.Register(provider.TypeAWS, Factory(Config{
		Creds: StaticCredentials{AccessKeyID: "AKIA", SecretAccessKey: "secret"},
		API:   newFakeAPI(),
	}))
	if !reg.Has(provider.TypeAWS) {
		t.Fatal("aws not registered")
	}
	p, err := reg.Resolve(provider.TypeAWS, map[string]any{
		"vpcCidr":            "10.9.0.0/16",
		"orphanGraceMinutes": float64(20),
		"accessKeyId":        "AKIA",
		"secretAccessKey":    "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestNoManagedServicesRequired(t *testing.T) {
	// Documented contract: adapter uses only EC2/EBS/VPC/EIP primitives.
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "eu-central-1"}, api)
	ctx := context.Background()
	node, err := p.CreateNode(ctx, "op_prim", provider.CreateNodeRequest{
		Name: "p-0", NodePool: "p", MachineType: "t3.small", Region: "eu-central-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range api.CallOrder() {
		switch c {
		case "instance.create", "instance.list", "instance.get", "instance.delete",
			"instance.reboot", "vpc.create", "vpc.delete", "vpc.list",
			"volume.create", "volume.attach", "volume.detach", "volume.delete", "volume.resize", "volume.list",
			"eip.allocate", "eip.associate", "eip.disassociate", "eip.release", "eip.list",
			"regions.describe", "instancetypes.describe", "pricing.get":
		default:
			t.Fatalf("unexpected API call (managed service?): %s", c)
		}
	}
	_ = node
}
