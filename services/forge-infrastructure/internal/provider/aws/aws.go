package aws

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

const nodeIDPrefix = "aws:"

// Config holds AWS provider runtime settings.
type Config struct {
	APIBase            string // optional EC2 endpoint template override
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

// Provider implements provider.Provider against AWS EC2 primitives.
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

// Factory returns a ProviderFactory for type "aws".
func Factory(defaults Config) provider.ProviderFactory {
	return func(cfg map[string]any) (provider.Provider, error) {
		merged := defaults
		spec, err := ParseConfig(cfg)
		if err != nil {
			return nil, err
		}
		merged.Spec = spec
		if v, ok := cfg["apiBase"].(string); ok && strings.TrimSpace(v) != "" {
			merged.APIBase = strings.TrimSpace(v)
		}
		if v, ok := cfg["defaultRegion"].(string); ok && strings.TrimSpace(v) != "" {
			merged.DefaultRegion = strings.TrimSpace(v)
		}
		if ak, ok := cfg["accessKeyId"].(string); ok {
			if sk, ok2 := cfg["secretAccessKey"].(string); ok2 {
				merged.Creds = StaticCredentials{
					AccessKeyID:     strings.TrimSpace(ak),
					SecretAccessKey: strings.TrimSpace(sk),
				}
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

// New constructs an AWS Provider.
func New(cfg Config) (*Provider, error) {
	spec := cfg.Spec
	if spec.VPCCIDR == "" {
		parsed, err := ParseConfig(nil)
		if err != nil {
			return nil, err
		}
		spec = parsed
		if cfg.Spec.OrphanGraceMinutes > 0 {
			spec.OrphanGraceMinutes = cfg.Spec.OrphanGraceMinutes
		}
		if cfg.Spec.AMI != "" {
			spec.AMI = cfg.Spec.AMI
		}
		if cfg.Spec.VPCCIDR != "" {
			spec.VPCCIDR = cfg.Spec.VPCCIDR
		}
		if cfg.Spec.SubnetCIDR != "" {
			spec.SubnetCIDR = cfg.Spec.SubnetCIDR
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
		hc := NewHTTPClient(creds, lim, log, cfg.DefaultRegion)
		if cfg.APIBase != "" {
			hc.EndpointTemplate = cfg.APIBase
		}
		api = hc
	}
	if cfg.DefaultRegion == "" {
		cfg.DefaultRegion = "eu-central-1"
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
	_, err := p.api.DescribeRegions(ctx)
	return err
}

func (p *Provider) ListRegions(ctx context.Context) ([]provider.Region, error) {
	regs, err := p.api.DescribeRegions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Region, 0, len(regs))
	for _, r := range regs {
		out = append(out, provider.Region{ID: r.ID, Name: r.Name})
	}
	return out, nil
}

func (p *Provider) ListMachineTypes(ctx context.Context, region string) ([]provider.MachineType, error) {
	types, err := p.api.DescribeInstanceTypes(ctx, region)
	if err != nil {
		return nil, err
	}
	out := make([]provider.MachineType, 0, len(types))
	for _, t := range types {
		out = append(out, provider.MachineType{
			ID:        t.ID,
			Name:      t.ID,
			CPU:       t.CPU,
			MemoryMiB: t.MemoryMiB,
			DiskGiB:   t.DiskGiB,
			GPU:       t.GPU,
			Region:    region,
		})
	}
	return out, nil
}

func (p *Provider) CreateNetwork(ctx context.Context, opID string, req provider.CreateNetworkRequest) (*provider.Network, error) {
	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	existing, err := p.api.DescribeVPCs(ctx, region, TagFilterOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		n := existing[0]
		return &provider.Network{ID: networkID(n.ID), Name: req.Name, Region: region, CIDR: n.CIDR}, nil
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "forge-net-" + shortName(opID)
	}
	cidr := req.CIDR
	if cidr == "" {
		cidr = p.spec.VPCCIDR
	}
	tags := map[string]string{
		TagManaged:  TagManagedValue,
		TagOpID:     opID,
		TagRole:     RoleNetwork,
		TagNodePool: name,
		TagName:     name,
	}
	created, err := p.api.CreateVPC(ctx, region, CreateVPCRequest{
		CIDR:       cidr,
		SubnetCIDR: p.spec.SubnetCIDR,
		Name:       name,
		Tags:       tags,
	})
	if err != nil {
		return nil, err
	}
	p.record("vpc.create")
	p.log.Info("aws network created",
		"event", "infra.provider.aws.create_network",
		"vpc_id", created.ID,
		"op_id", opID,
	)
	return &provider.Network{ID: networkID(created.ID), Name: name, Region: region, CIDR: created.CIDR}, nil
}

func (p *Provider) DeleteNetwork(ctx context.Context, opID string, netID string) error {
	vpcID, err := parseResourceID(netID, "aws-net:")
	if err != nil {
		return err
	}
	region := p.cfg.DefaultRegion
	if err := p.api.DeleteVPC(ctx, region, vpcID); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("vpc.delete")
	p.log.Info("aws network deleted",
		"event", "infra.provider.aws.delete_network",
		"vpc_id", vpcID,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	p.log.Info("aws create_node start",
		"event", "infra.provider.aws.create_node",
		"op_id", opID,
		"node_pool", req.NodePool,
		"machine_type", req.MachineType,
	)

	unlock := p.lockOp(opID)
	defer unlock()

	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}

	if existing, err := p.findInstanceByOpID(ctx, region, opID); err != nil {
		return nil, err
	} else if existing != nil {
		p.record("instance.adopt")
		return existing, nil
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
	tags[TagName] = sanitizeName(name)

	ami := p.spec.AMI
	if ami == "" {
		ami = defaultAMI
	}

	created, err := p.api.RunInstances(ctx, region, RunInstancesRequest{
		Name:         sanitizeName(name),
		InstanceType: req.MachineType,
		AMI:          ami,
		UserData:     req.UserData,
		ClientToken:  opID, // native AWS idempotency key
		Tags:         tags,
	})
	if err != nil {
		if existing, findErr := p.findInstanceByOpID(ctx, region, opID); findErr == nil && existing != nil {
			p.record("instance.adopt")
			return existing, nil
		}
		return nil, err
	}
	p.record("instance.create")
	node := instanceToNode(created, region)
	p.log.Info("aws create_node ok",
		"event", "infra.provider.aws.create_node",
		"instance_id", created.ID,
		"node_pool", pool,
		"action", "create",
		"op_id", opID,
	)
	return node, nil
}

func (p *Provider) DeleteNode(ctx context.Context, opID string, nodeID string) error {
	return p.teardownNode(ctx, opID, nodeID)
}

func (p *Provider) RebootNode(ctx context.Context, opID string, nodeID string) error {
	id, region, err := parseNodeID(nodeID)
	if err != nil {
		return err
	}
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	if err := p.api.RebootInstance(ctx, region, id); err != nil {
		return err
	}
	p.record("instance.reboot")
	p.log.Info("aws reboot_node ok",
		"event", "infra.provider.aws.reboot_node",
		"instance_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) GetNode(ctx context.Context, nodeID string) (*provider.ProviderNode, error) {
	id, region, err := parseNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	inst, err := p.api.GetInstance(ctx, region, id)
	if err != nil {
		return nil, err
	}
	return instanceToNode(inst, region), nil
}

func (p *Provider) ListNodes(ctx context.Context) ([]provider.ProviderNode, error) {
	region := p.cfg.DefaultRegion
	insts, err := p.api.DescribeInstances(ctx, region, TagFilterManaged())
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProviderNode, 0, len(insts))
	for i := range insts {
		out = append(out, *instanceToNode(&insts[i], region))
	}
	return out, nil
}

func (p *Provider) AttachDisk(ctx context.Context, opID string, nodeID string, req provider.AttachDiskRequest) (*provider.Disk, error) {
	instID, region, err := parseNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	inst, err := p.api.GetInstance(ctx, region, instID)
	if err != nil {
		return nil, err
	}
	size := req.SizeGiB
	if size < 1 {
		size = 10
	}
	az := inst.AZ
	if az == "" {
		az = region + "a"
	}
	pool := ""
	if inst.Tags != nil {
		pool = inst.Tags[TagNodePool]
	}
	name := req.Name
	if name == "" {
		name = "forge-vol-" + shortName(opID)
	}
	vol, err := p.api.CreateVolume(ctx, region, CreateVolumeRequest{
		SizeGiB:    size,
		AZ:         az,
		Name:       name,
		InstanceID: instID,
		Tags: map[string]string{
			TagManaged:  TagManagedValue,
			TagOpID:     opID,
			TagNodePool: pool,
			TagRole:     RoleVolume,
			TagName:     sanitizeName(name),
		},
	})
	if err != nil {
		return nil, err
	}
	p.record("volume.create")
	if vol.InstanceID == "" {
		if err := p.api.AttachVolume(ctx, region, vol.ID, instID); err != nil {
			return nil, err
		}
		p.record("volume.attach")
		vol.InstanceID = instID
	} else {
		p.record("volume.attach")
	}
	return &provider.Disk{
		ID:       volumeID(vol.ID),
		NodeID:   nodeID,
		SizeGiB:  vol.SizeGiB,
		Attached: true,
	}, nil
}

func (p *Provider) DetachDisk(ctx context.Context, opID string, nodeID string, diskID string) error {
	_ = nodeID
	id, err := parseResourceID(diskID, "aws-vol:")
	if err != nil {
		return err
	}
	region := p.cfg.DefaultRegion
	if err := p.api.DetachVolume(ctx, region, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("volume.detach")
	p.log.Info("aws detach_disk",
		"event", "infra.provider.aws.detach_disk",
		"volume_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) ResizeDisk(ctx context.Context, opID string, diskID string, newSizeGiB int) error {
	id, err := parseResourceID(diskID, "aws-vol:")
	if err != nil {
		return err
	}
	region := p.cfg.DefaultRegion
	if err := p.api.ModifyVolume(ctx, region, id, newSizeGiB); err != nil {
		return err
	}
	p.record("volume.resize")
	p.log.Info("aws resize_disk",
		"event", "infra.provider.aws.resize_disk",
		"volume_id", id,
		"new_size_gib", newSizeGiB,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) CreatePublicIP(ctx context.Context, opID string, req provider.CreatePublicIPRequest) (*provider.PublicIP, error) {
	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	existing, err := p.api.DescribeAddresses(ctx, region, TagFilterOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		ip := existing[0]
		return &provider.PublicIP{ID: eipID(ip.AllocationID), Address: ip.PublicIP, NodeID: req.NodeID}, nil
	}
	name := req.Name
	if name == "" {
		name = "forge-ip-" + shortName(opID)
	}
	instID := ""
	if req.NodeID != "" {
		if id, _, err := parseNodeID(req.NodeID); err == nil {
			instID = id
		}
	}
	ip, err := p.api.AllocateAddress(ctx, region, AllocateAddressRequest{
		Name:       name,
		InstanceID: instID,
		Tags: map[string]string{
			TagManaged: TagManagedValue,
			TagOpID:    opID,
			TagRole:    RoleElasticIP,
			TagName:    sanitizeName(name),
		},
	})
	if err != nil {
		return nil, err
	}
	p.record("eip.allocate")
	if instID != "" && ip.InstanceID == "" {
		if err := p.api.AssociateAddress(ctx, region, ip.AllocationID, instID); err != nil {
			return nil, err
		}
		p.record("eip.associate")
	}
	return &provider.PublicIP{ID: eipID(ip.AllocationID), Address: ip.PublicIP, NodeID: req.NodeID}, nil
}

func (p *Provider) DeletePublicIP(ctx context.Context, opID string, ipID string) error {
	id, err := parseResourceID(ipID, "aws-eip:")
	if err != nil {
		return err
	}
	region := p.cfg.DefaultRegion
	addrs, _ := p.api.DescribeAddresses(ctx, region, TagFilterManaged())
	for _, a := range addrs {
		if a.AllocationID == id && a.AssociationID != "" {
			_ = p.api.DisassociateAddress(ctx, region, a.AssociationID)
			p.record("eip.disassociate")
		}
	}
	if err := p.api.ReleaseAddress(ctx, region, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("eip.release")
	p.log.Info("aws delete_public_ip",
		"event", "infra.provider.aws.delete_public_ip",
		"allocation_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) GetPricing(ctx context.Context, region string, machineType string) (*provider.Pricing, error) {
	hourly, err := p.api.GetPricing(ctx, region, machineType)
	if err != nil {
		return nil, err
	}
	return &provider.Pricing{
		Region:      region,
		MachineType: machineType,
		HourlyUSD:   hourly,
		Currency:    "USD",
	}, nil
}

// OrphansDeleted returns how many orphan resources this provider has deleted.
func (p *Provider) OrphansDeleted() int64 { return p.orphansDeleted.Load() }

// CallOrder returns recorded API call names when recording is enabled (tests).
func (p *Provider) CallOrder() []string {
	return append([]string(nil), p.callOrder...)
}

// EnableCallRecording turns on teardown/create call order capture (tests).
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

func (p *Provider) findInstanceByOpID(ctx context.Context, region, opID string) (*provider.ProviderNode, error) {
	insts, err := p.api.DescribeInstances(ctx, region, TagFilterOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(insts) == 0 {
		return nil, nil
	}
	return instanceToNode(&insts[0], region), nil
}

func instanceToNode(inst *Instance, region string) *provider.ProviderNode {
	addr := inst.PublicIP
	if addr == "" {
		addr = inst.PrivateIP
	}
	r := inst.Region
	if r == "" {
		r = region
	}
	mt := inst.InstanceType
	if mt == "" && inst.Tags != nil {
		mt = inst.Tags["forge.machine_type"]
	}
	phase := "Provisioning"
	switch strings.ToLower(inst.State) {
	case "running":
		phase = "Ready"
	case "stopped", "stopping":
		phase = "Stopped"
	case "terminated", "shutting-down":
		phase = "Deleting"
	case "pending":
		phase = "Provisioning"
	}
	labels := inst.Tags
	if labels == nil {
		labels = map[string]string{}
	}
	return &provider.ProviderNode{
		ID:          nodeIDPrefix + inst.ID,
		Name:        inst.Name,
		Region:      r,
		MachineType: mt,
		Address:     addr,
		Phase:       phase,
		Labels:      labels,
	}
}

// parseNodeID returns (instanceID, regionHint, error). Region may be empty.
func parseNodeID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, nodeIDPrefix)
	if id == "" || !strings.HasPrefix(id, "i-") {
		// allow bare i-xxx or any non-empty id from fakes
		if id == "" {
			return "", "", fmt.Errorf("invalid aws node id %q", id)
		}
	}
	return id, "", nil
}

func parseResourceID(id, prefix string) (string, error) {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, prefix)
	if id == "" {
		return "", fmt.Errorf("invalid resource id %q", id)
	}
	return id, nil
}

func networkID(id string) string { return "aws-net:" + id }
func volumeID(id string) string  { return "aws-vol:" + id }
func eipID(id string) string     { return "aws-eip:" + id }

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
