package hetzner

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"forge.local/services/forge-infrastructure/internal/provider"
)

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(map[string]any{
		"networkCIDR":        "10.1.0.0/16",
		"orphanGraceMinutes": 15,
	}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if err := ValidateConfig(map[string]any{"networkCIDR": "not-a-cidr"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
	if err := ValidateConfig(map[string]any{"orphanGraceMinutes": 0}); err == nil {
		t.Fatal("expected orphanGraceMinutes error")
	}
	if err := ValidateConfig(map[string]any{"orphanGraceMinutes": -3}); err == nil {
		t.Fatal("expected orphanGraceMinutes error")
	}
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OrphanGraceMinutes != 15 || cfg.NetworkCIDR != "10.1.0.0/16" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestCreateNodeIdempotentByOpID(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "fsn1", Spec: SpecConfig{Image: "ubuntu-24.04"}}, api)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name: "pool-a-0", NodePool: "pool-a", MachineType: "cx22", Region: "fsn1",
	}
	n1, err := p.CreateNode(ctx, "op_retry1", req)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n1.ID == "" || n1.Address == "" || n1.MachineType != "cx22" {
		t.Fatalf("node: %+v", n1)
	}
	if n1.Labels[LabelManaged] != "true" || n1.Labels[LabelOpID] != "op_retry1" {
		t.Fatalf("labels: %+v", n1.Labels)
	}
	createsBefore := 0
	for _, c := range api.CallOrder() {
		if c == "server.create" {
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
		if c == "server.create" {
			createsAfter++
		}
	}
	if createsAfter != createsBefore {
		t.Fatalf("expected no second POST /servers, creates before=%d after=%d calls=%v", createsBefore, createsAfter, api.CallOrder())
	}
	if api.serverCount() != 1 {
		t.Fatalf("expected 1 server, got %d", api.serverCount())
	}
}

func TestCreateNodeConcurrentSameOpID(t *testing.T) {
	api := newFakeAPI()
	api.createDelay = 20 * time.Millisecond
	p := NewWithAPI(Config{DefaultRegion: "fsn1"}, api)
	ctx := context.Background()
	req := provider.CreateNodeRequest{
		Name: "pool-b-0", NodePool: "pool-b", MachineType: "cx22", Region: "fsn1",
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
	if api.serverCount() != 1 {
		t.Fatalf("expected exactly 1 server, got %d", api.serverCount())
	}
	creates := 0
	for _, c := range api.CallOrder() {
		if c == "server.create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("expected 1 server.create, got %d (%v)", creates, api.CallOrder())
	}
}

func TestRateLimitBackoffIncreasesAndRespectsReset(t *testing.T) {
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
	if len(sleeps) < 2 {
		t.Fatalf("expected 2 sleeps, got %v", sleeps)
	}
	if sleeps[1] <= sleeps[0] {
		t.Fatalf("expected increasing backoff, got %v then %v", sleeps[0], sleeps[1])
	}
	// RateLimit-Reset far in the future should dominate.
	resetAt := base.Add(5 * time.Second)
	h2 := http.Header{}
	h2.Set("RateLimit-Reset", "1700000005")
	_ = resetAt
	sleeps = nil
	lim.consecutive = 0
	if err := lim.Backoff429(ctx, h2); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] < 4*time.Second {
		t.Fatalf("expected delay respecting RateLimit-Reset (~5s), got %v", sleeps)
	}
}

func TestTeardownDeleteOrder(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "fsn1"}, api)
	p.EnableCallRecording()
	ctx := context.Background()

	// Create network for pool.
	net, err := p.CreateNetwork(ctx, "op_net", provider.CreateNetworkRequest{
		Name: "pool-t", CIDR: "10.2.0.0/16", Region: "fsn1",
	})
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	// Manually label network with nodepool for last-node cleanup.
	api.mu.Lock()
	for _, n := range api.networks {
		n.Labels[LabelNodePool] = "pool-t"
	}
	api.mu.Unlock()
	_ = net

	node, err := p.CreateNode(ctx, "op_node", provider.CreateNodeRequest{
		Name: "pool-t-0", NodePool: "pool-t", MachineType: "cx22", Region: "fsn1",
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	disk, err := p.AttachDisk(ctx, "op_disk", node.ID, provider.AttachDiskRequest{SizeGiB: 20, Name: "data"})
	if err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}
	ip, err := p.CreatePublicIP(ctx, "op_ip", provider.CreatePublicIPRequest{
		Region: "fsn1", NodeID: node.ID, Name: "stable",
	})
	if err != nil {
		t.Fatalf("CreatePublicIP: %v", err)
	}
	_ = disk
	_ = ip

	p.callOrder = nil
	api.mu.Lock()
	api.calls = nil
	api.mu.Unlock()

	if err := p.DeleteNode(ctx, "op_del", node.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	order := api.CallOrder()
	// Required sequence: volume.detach, volume.delete, floating_ip.unassign/delete, server.delete, network.delete
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
	fu := idx("floating_ip.unassign")
	fdel := idx("floating_ip.delete")
	sd := idx("server.delete")
	nd := idx("network.delete")
	if vd < 0 || vdel < 0 || fu < 0 || fdel < 0 || sd < 0 || nd < 0 {
		t.Fatalf("missing teardown steps in call order: %v", order)
	}
	if !(vd < vdel && vdel < fu && fu < fdel && fdel < sd && sd < nd) {
		t.Fatalf("teardown order wrong: %v (vd=%d vdel=%d fu=%d fdel=%d sd=%d nd=%d)", order, vd, vdel, fu, fdel, sd, nd)
	}
	if api.serverCount() != 0 || api.volumeCount() != 0 || api.floatingCount() != 0 || api.networkCount() != 0 {
		t.Fatalf("leaked resources: servers=%d vols=%d ips=%d nets=%d",
			api.serverCount(), api.volumeCount(), api.floatingCount(), api.networkCount())
	}
}

func TestReconcileOrphansGracePeriod(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{
		DefaultRegion: "fsn1",
		Spec:          SpecConfig{OrphanGraceMinutes: 15},
	}, api)
	ctx := context.Background()

	// Tracked node — must not be deleted.
	tracked, err := p.CreateNode(ctx, "op_tracked", provider.CreateNodeRequest{
		Name: "pool-o-0", NodePool: "pool-o", MachineType: "cx22", Region: "fsn1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Orphan past grace.
	old, err := p.CreateNode(ctx, "op_old", provider.CreateNodeRequest{
		Name: "pool-o-1", NodePool: "pool-o", MachineType: "cx22", Region: "fsn1",
	})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, s := range api.servers {
		if nodeIDPrefix+itoa(s.ID) == old.ID {
			s.Created = time.Now().UTC().Add(-20 * time.Minute).Format(time.RFC3339)
		}
	}
	api.mu.Unlock()

	// Orphan within grace.
	fresh, err := p.CreateNode(ctx, "op_fresh", provider.CreateNodeRequest{
		Name: "pool-o-2", NodePool: "pool-o", MachineType: "cx22", Region: "fsn1",
	})
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	for _, s := range api.servers {
		if nodeIDPrefix+itoa(s.ID) == fresh.ID {
			s.Created = time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
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
	if _, err := api.GetServer(ctx, mustParseID(old.ID)); err == nil || !IsNotFound(err) {
		t.Fatalf("old orphan should be deleted, err=%v", err)
	}
	if _, err := api.GetServer(ctx, mustParseID(fresh.ID)); err != nil {
		t.Fatalf("fresh orphan within grace should remain: %v", err)
	}
	if _, err := api.GetServer(ctx, mustParseID(tracked.ID)); err != nil {
		t.Fatalf("tracked node should remain: %v", err)
	}
}

func TestLifecycleCreateDeleteNoLeak(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{DefaultRegion: "fsn1"}, api)
	ctx := context.Background()
	node, err := p.CreateNode(ctx, "op_life", provider.CreateNodeRequest{
		Name: "life-0", NodePool: "life", MachineType: "cx22", Region: "fsn1",
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
	_, err = p.AttachDisk(ctx, "op_vol", node.ID, provider.AttachDiskRequest{SizeGiB: 10})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.CreatePublicIP(ctx, "op_fip", provider.CreatePublicIPRequest{Region: "fsn1", NodeID: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteNode(ctx, "op_del", node.ID); err != nil {
		t.Fatal(err)
	}
	if api.serverCount()+api.volumeCount()+api.floatingCount() != 0 {
		t.Fatalf("leaks after DeleteNode: servers=%d vols=%d ips=%d",
			api.serverCount(), api.volumeCount(), api.floatingCount())
	}
}

func TestRegistryRegistersHetzner(t *testing.T) {
	reg := provider.NewRegistry(nil)
	reg.Register(provider.TypeHetzner, Factory(Config{
		Tokens: StaticToken("test-token"),
		API:    newFakeAPI(),
	}))
	if !reg.Has(provider.TypeHetzner) {
		t.Fatal("hetzner not registered")
	}
	p, err := reg.Resolve(provider.TypeHetzner, map[string]any{
		"networkCIDR":        "10.9.0.0/16",
		"orphanGraceMinutes": float64(20),
		"apiToken":           "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestGetPricing(t *testing.T) {
	api := newFakeAPI()
	p := NewWithAPI(Config{}, api)
	pr, err := p.GetPricing(context.Background(), "fsn1", "cx22")
	if err != nil {
		t.Fatal(err)
	}
	if pr.HourlyUSD <= 0 {
		t.Fatalf("expected hourly price, got %+v", pr)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func mustParseID(nodeID string) int64 {
	id, err := parseNodeID(nodeID)
	if err != nil {
		panic(err)
	}
	return id
}
