package azure

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"forge.local/services/forge-infrastructure/internal/provider"
)

const nodeIDPrefix = "azure:"

// Config holds Azure provider runtime settings.
type Config struct {
	ARMBase            string
	MaxConcurrentOps   int
	OrphanScanInterval time.Duration
	DefaultRegion      string
	Spec               SpecConfig
	Creds              CredentialSource
	CredentialResolver CredentialResolver
	API                API
	Limiter            *Limiter
	Log                *slog.Logger
}

// Provider implements provider.Provider against Azure VM primitives.
type Provider struct {
	cfg     Config
	api     API
	limiter *Limiter
	log     *slog.Logger
	spec    SpecConfig

	createMu   sync.Mutex
	inflightOp map[string]*sync.Mutex

	orphansDeleted atomic.Int64
	callOrder      []string
	recordCalls    bool
}

// Factory returns a ProviderFactory for type "azure".
func Factory(defaults Config) provider.ProviderFactory {
	return func(cfg map[string]any) (provider.Provider, error) {
		merged := defaults
		spec, err := ParseConfig(cfg)
		if err != nil {
			return nil, err
		}
		merged.Spec = spec
		if v, ok := cfg["armBase"].(string); ok && strings.TrimSpace(v) != "" {
			merged.ARMBase = strings.TrimSpace(v)
		}
		if v, ok := cfg["defaultRegion"].(string); ok && strings.TrimSpace(v) != "" {
			merged.DefaultRegion = strings.TrimSpace(v)
		}
		if tid, ok := cfg["tenantId"].(string); ok {
			merged.Creds = StaticCredentials{
				TenantID:       strings.TrimSpace(tid),
				ClientID:       stringField(cfg, "clientId"),
				ClientSecret:   stringField(cfg, "clientSecret"),
				SubscriptionID: stringField(cfg, "subscriptionId"),
			}
		}
		if ref, ok := cfg["credentialsSecretRef"].(map[string]any); ok {
			if name, _ := ref["name"].(string); strings.TrimSpace(name) != "" {
				merged.Creds = SecretCredentials{Name: strings.TrimSpace(name), Resolver: merged.CredentialResolver}
			}
		}
		if name, ok := cfg["credentialsSecretName"].(string); ok && strings.TrimSpace(name) != "" {
			merged.Creds = SecretCredentials{Name: strings.TrimSpace(name), Resolver: merged.CredentialResolver}
		}
		return New(merged)
	}
}

func stringField(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// New constructs an Azure Provider.
func New(cfg Config) (*Provider, error) {
	spec := cfg.Spec
	if spec.VNetCIDR == "" {
		parsed, err := ParseConfig(nil)
		if err != nil {
			return nil, err
		}
		spec = parsed
		if cfg.Spec.OrphanGraceMinutes > 0 {
			spec.OrphanGraceMinutes = cfg.Spec.OrphanGraceMinutes
		}
		if cfg.Spec.Image != "" {
			spec.Image = cfg.Spec.Image
		}
		if cfg.Spec.VNetCIDR != "" {
			spec.VNetCIDR = cfg.Spec.VNetCIDR
		}
		if cfg.Spec.SubnetCIDR != "" {
			spec.SubnetCIDR = cfg.Spec.SubnetCIDR
		}
		if cfg.Spec.ResourceGroup != "" {
			spec.ResourceGroup = cfg.Spec.ResourceGroup
		}
	}
	lim := cfg.Limiter
	if lim == nil {
		lim = NewLimiter(cfg.MaxConcurrentOps)
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	creds := cfg.Creds
	if creds == nil {
		creds = StaticCredentials{}
	}
	api := cfg.API
	if api == nil {
		hc := NewHTTPClient(creds, lim, log, cfg.DefaultRegion, spec.ResourceGroup)
		if cfg.ARMBase != "" {
			hc.ARMBase = cfg.ARMBase
		}
		api = hc
	}
	if cfg.DefaultRegion == "" {
		cfg.DefaultRegion = "westeurope"
	}
	return &Provider{
		cfg:        cfg,
		api:        api,
		limiter:    lim,
		log:        log,
		spec:       spec,
		inflightOp: map[string]*sync.Mutex{},
	}, nil
}

// NewWithAPI constructs a Provider with an injected API (tests).
func NewWithAPI(cfg Config, api API) *Provider {
	cfg.API = api
	p, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return p
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	_, err := p.api.ListLocations(ctx)
	return err
}

func (p *Provider) ListRegions(ctx context.Context) ([]provider.Region, error) {
	locs, err := p.api.ListLocations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Region, 0, len(locs))
	for _, l := range locs {
		name := l.DisplayName
		if name == "" {
			name = l.Name
		}
		out = append(out, provider.Region{ID: l.Name, Name: name})
	}
	return out, nil
}

func (p *Provider) ListMachineTypes(ctx context.Context, region string) ([]provider.MachineType, error) {
	sizes, err := p.api.ListVMSizes(ctx, region)
	if err != nil {
		return nil, err
	}
	out := make([]provider.MachineType, 0, len(sizes))
	for _, s := range sizes {
		out = append(out, provider.MachineType{
			ID: s.Name, Name: s.Name, CPU: s.CPU, MemoryMiB: s.MemoryMiB,
			DiskGiB: s.DiskGiB, GPU: s.GPU, Region: region,
		})
	}
	return out, nil
}

func (p *Provider) CreateNetwork(ctx context.Context, opID string, req provider.CreateNetworkRequest) (*provider.Network, error) {
	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	existing, err := p.api.ListVNets(ctx, TagFilterOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		n := existing[0]
		return &provider.Network{ID: networkID(n.ID), Name: n.Name, Region: region, CIDR: n.CIDR}, nil
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "forge-net-" + shortName(opID)
	}
	cidr := req.CIDR
	if cidr == "" {
		cidr = p.spec.VNetCIDR
	}
	created, err := p.api.CreateVNet(ctx, CreateVNetRequest{
		Name: sanitizeName(name), Location: region, CIDR: cidr, SubnetCIDR: p.spec.SubnetCIDR,
		Tags: map[string]string{
			TagManaged: TagManagedValue, TagOpID: opID, TagRole: RoleNetwork, TagNodePool: name,
		},
	})
	if err != nil {
		return nil, err
	}
	p.record("vnet.create")
	p.log.Info("azure network created", "event", "infra.provider.azure.create_network", "vnet_id", created.ID, "op_id", opID)
	return &provider.Network{ID: networkID(created.ID), Name: created.Name, Region: region, CIDR: created.CIDR}, nil
}

func (p *Provider) DeleteNetwork(ctx context.Context, opID string, netID string) error {
	id, err := parseResourceID(netID, "azure-net:")
	if err != nil {
		return err
	}
	if err := p.api.DeleteVNet(ctx, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("vnet.delete")
	p.log.Info("azure network deleted", "event", "infra.provider.azure.delete_network", "vnet_id", id, "op_id", opID)
	return nil
}

func (p *Provider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	p.log.Info("azure create_node start",
		"event", "infra.provider.azure.create_node",
		"op_id", opID, "node_pool", req.NodePool, "machine_type", req.MachineType,
	)
	unlock := p.lockOp(opID)
	defer unlock()

	if existing, err := p.findVMByOpID(ctx, opID); err != nil {
		return nil, err
	} else if existing != nil {
		p.record("vm.adopt")
		return existing, nil
	}

	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	pool := req.NodePool
	if pool == "" {
		pool = req.Name
	}
	tags := ManagedTags(pool, opID)
	for k, v := range req.Labels {
		if _, reserved := tags[k]; !reserved {
			tags[k] = v
		}
	}
	tags["forge.machine_type"] = req.MachineType
	name := req.Name
	if name == "" {
		name = "forge-" + shortName(pool) + "-" + shortName(opID)
	}
	image := p.spec.Image
	if image == "" {
		image = defaultImage
	}

	created, err := p.api.CreateVM(ctx, CreateVMRequest{
		Name: sanitizeName(name), Location: region, Size: req.MachineType,
		Image: image, UserData: req.UserData, Tags: tags,
	})
	if err != nil {
		if existing, findErr := p.findVMByOpID(ctx, opID); findErr == nil && existing != nil {
			p.record("vm.adopt")
			return existing, nil
		}
		return nil, err
	}
	p.record("vm.create")
	node := vmToNode(created)
	p.log.Info("azure create_node ok",
		"event", "infra.provider.azure.create_node",
		"vm_id", created.ID, "node_pool", pool, "action", "create", "op_id", opID,
	)
	return node, nil
}

func (p *Provider) DeleteNode(ctx context.Context, opID string, nodeID string) error {
	return p.teardownNode(ctx, opID, nodeID)
}

func (p *Provider) RebootNode(ctx context.Context, opID string, nodeID string) error {
	id, err := parseNodeID(nodeID)
	if err != nil {
		return err
	}
	if err := p.api.RestartVM(ctx, id); err != nil {
		return err
	}
	p.record("vm.restart")
	p.log.Info("azure reboot_node ok", "event", "infra.provider.azure.reboot_node", "vm_id", id, "op_id", opID)
	return nil
}

func (p *Provider) GetNode(ctx context.Context, nodeID string) (*provider.ProviderNode, error) {
	id, err := parseNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	vm, err := p.api.GetVM(ctx, id)
	if err != nil {
		return nil, err
	}
	return vmToNode(vm), nil
}

func (p *Provider) ListNodes(ctx context.Context) ([]provider.ProviderNode, error) {
	vms, err := p.api.ListVMs(ctx, TagFilterManaged())
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProviderNode, 0, len(vms))
	for i := range vms {
		out = append(out, *vmToNode(&vms[i]))
	}
	return out, nil
}

func (p *Provider) AttachDisk(ctx context.Context, opID string, nodeID string, req provider.AttachDiskRequest) (*provider.Disk, error) {
	vmID, err := parseNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	vm, err := p.api.GetVM(ctx, vmID)
	if err != nil {
		return nil, err
	}
	size := req.SizeGiB
	if size < 1 {
		size = 10
	}
	name := req.Name
	if name == "" {
		name = "forge-disk-" + shortName(opID)
	}
	pool := ""
	if vm.Tags != nil {
		pool = vm.Tags[TagNodePool]
	}
	loc := vm.Location
	if loc == "" {
		loc = p.cfg.DefaultRegion
	}
	disk, err := p.api.CreateDisk(ctx, CreateDiskRequest{
		Name: sanitizeName(name), Location: loc, SizeGiB: size, VMID: vmID,
		Tags: map[string]string{
			TagManaged: TagManagedValue, TagOpID: opID, TagNodePool: pool, TagRole: RoleVolume,
		},
	})
	if err != nil {
		return nil, err
	}
	p.record("disk.create")
	if disk.VMID == "" {
		if err := p.api.AttachDisk(ctx, disk.ID, vmID); err != nil {
			return nil, err
		}
		p.record("disk.attach")
		disk.VMID = vmID
	} else {
		p.record("disk.attach")
	}
	return &provider.Disk{ID: diskID(disk.ID), NodeID: nodeID, SizeGiB: disk.SizeGiB, Attached: true}, nil
}

func (p *Provider) DetachDisk(ctx context.Context, opID string, nodeID string, dID string) error {
	_ = nodeID
	id, err := parseResourceID(dID, "azure-disk:")
	if err != nil {
		return err
	}
	if err := p.api.DetachDisk(ctx, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("disk.detach")
	p.log.Info("azure detach_disk", "event", "infra.provider.azure.detach_disk", "disk_id", id, "op_id", opID)
	return nil
}

func (p *Provider) ResizeDisk(ctx context.Context, opID string, dID string, newSizeGiB int) error {
	id, err := parseResourceID(dID, "azure-disk:")
	if err != nil {
		return err
	}
	if err := p.api.ResizeDisk(ctx, id, newSizeGiB); err != nil {
		return err
	}
	p.record("disk.resize")
	p.log.Info("azure resize_disk", "event", "infra.provider.azure.resize_disk", "disk_id", id, "new_size_gib", newSizeGiB, "op_id", opID)
	return nil
}

func (p *Provider) CreatePublicIP(ctx context.Context, opID string, req provider.CreatePublicIPRequest) (*provider.PublicIP, error) {
	existing, err := p.api.ListPublicIPs(ctx, TagFilterOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		ip := existing[0]
		return &provider.PublicIP{ID: publicIPID(ip.ID), Address: ip.Address, NodeID: req.NodeID}, nil
	}
	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	name := req.Name
	if name == "" {
		name = "forge-ip-" + shortName(opID)
	}
	vmID := ""
	if req.NodeID != "" {
		if id, err := parseNodeID(req.NodeID); err == nil {
			vmID = id
		}
	}
	ip, err := p.api.CreatePublicIP(ctx, CreatePublicIPAPIRequest{
		Name: sanitizeName(name), Location: region, VMID: vmID,
		Tags: map[string]string{TagManaged: TagManagedValue, TagOpID: opID, TagRole: RolePublicIP},
	})
	if err != nil {
		return nil, err
	}
	p.record("pip.create")
	if vmID != "" && ip.VMID == "" {
		if err := p.api.AssociatePublicIP(ctx, ip.ID, vmID); err != nil {
			return nil, err
		}
		p.record("pip.associate")
	}
	return &provider.PublicIP{ID: publicIPID(ip.ID), Address: ip.Address, NodeID: req.NodeID}, nil
}

func (p *Provider) DeletePublicIP(ctx context.Context, opID string, ipID string) error {
	id, err := parseResourceID(ipID, "azure-ip:")
	if err != nil {
		return err
	}
	_ = p.api.DisassociatePublicIP(ctx, id)
	p.record("pip.disassociate")
	if err := p.api.DeletePublicIP(ctx, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("pip.delete")
	p.log.Info("azure delete_public_ip", "event", "infra.provider.azure.delete_public_ip", "ip_id", id, "op_id", opID)
	return nil
}

func (p *Provider) GetPricing(ctx context.Context, region string, machineType string) (*provider.Pricing, error) {
	hourly, err := p.api.GetPricing(ctx, region, machineType)
	if err != nil {
		return nil, err
	}
	return &provider.Pricing{Region: region, MachineType: machineType, HourlyUSD: hourly, Currency: "USD"}, nil
}

func (p *Provider) OrphansDeleted() int64 { return p.orphansDeleted.Load() }

func (p *Provider) CallOrder() []string {
	return append([]string(nil), p.callOrder...)
}

func (p *Provider) EnableCallRecording() { p.recordCalls = true }

func (p *Provider) record(name string) {
	if !p.recordCalls {
		return
	}
	p.callOrder = append(p.callOrder, name)
}

func (p *Provider) lockOp(opID string) func() {
	p.createMu.Lock()
	m, ok := p.inflightOp[opID]
	if !ok {
		m = &sync.Mutex{}
		p.inflightOp[opID] = m
	}
	p.createMu.Unlock()
	m.Lock()
	return m.Unlock
}

func (p *Provider) findVMByOpID(ctx context.Context, opID string) (*provider.ProviderNode, error) {
	vms, err := p.api.ListVMs(ctx, TagFilterOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(vms) == 0 {
		return nil, nil
	}
	return vmToNode(&vms[0]), nil
}

func vmToNode(vm *VM) *provider.ProviderNode {
	addr := vm.PublicIP
	if addr == "" {
		addr = vm.PrivateIP
	}
	mt := vm.Size
	if mt == "" && vm.Tags != nil {
		mt = vm.Tags["forge.machine_type"]
	}
	phase := "Provisioning"
	switch strings.ToLower(vm.PowerState) {
	case "running", "vm running":
		phase = "Ready"
	case "stopped", "deallocated", "vm deallocated":
		phase = "Stopped"
	case "deleting":
		phase = "Deleting"
	case "starting", "creating":
		phase = "Provisioning"
	}
	labels := vm.Tags
	if labels == nil {
		labels = map[string]string{}
	}
	return &provider.ProviderNode{
		ID: nodeIDPrefix + vm.ID, Name: vm.Name, Region: vm.Location,
		MachineType: mt, Address: addr, Phase: phase, Labels: labels,
	}
}

func parseNodeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, nodeIDPrefix)
	if id == "" {
		return "", fmt.Errorf("invalid azure node id")
	}
	return id, nil
}

func parseResourceID(id, prefix string) (string, error) {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, prefix)
	if id == "" {
		return "", fmt.Errorf("invalid resource id %q", id)
	}
	return id, nil
}

func networkID(id string) string  { return "azure-net:" + id }
func diskID(id string) string     { return "azure-disk:" + id }
func publicIPID(id string) string { return "azure-ip:" + id }

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "forge"
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

func shortName(s string) string {
	s = sanitizeName(s)
	if len(s) > 12 {
		return s[len(s)-12:]
	}
	return s
}

var _ provider.Provider = (*Provider)(nil)
