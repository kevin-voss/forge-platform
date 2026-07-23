package hetzner

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"forge.local/services/forge-infrastructure/internal/provider"
)

const nodeIDPrefix = "hetzner:"

// Config holds Hetzner provider runtime settings.
type Config struct {
	APIBase            string
	MaxConcurrentOps   int
	OrphanScanInterval time.Duration
	DefaultRegion      string
	Spec               SpecConfig
	Tokens             TokenSource
	TokenResolver      TokenResolver // used when cfg has credentialsSecretRef
	API                API           // optional inject for tests
	Limiter            *Limiter
	Log                *slog.Logger
}

// Provider implements provider.Provider against Hetzner Cloud.
type Provider struct {
	cfg     Config
	api     API
	limiter *Limiter
	log     *slog.Logger
	spec    SpecConfig

	createMu   sync.Mutex
	inflightOp map[string]*sync.Mutex // per-op serialize for concurrent CreateNode

	orphansDeleted atomic.Int64
	callOrder      []string // test-only when recording
	recordCalls    bool
}

// Factory returns a ProviderFactory for type "hetzner".
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
		if v, ok := cfg["apiToken"].(string); ok && strings.TrimSpace(v) != "" {
			merged.Tokens = StaticToken(strings.TrimSpace(v))
		}
		if ref, ok := cfg["credentialsSecretRef"].(map[string]any); ok {
			if name, _ := ref["name"].(string); strings.TrimSpace(name) != "" {
				merged.Tokens = SecretToken{Name: strings.TrimSpace(name), Resolver: merged.TokenResolver}
			}
		}
		if name, ok := cfg["credentialsSecretName"].(string); ok && strings.TrimSpace(name) != "" {
			merged.Tokens = SecretToken{Name: strings.TrimSpace(name), Resolver: merged.TokenResolver}
		}
		if v, ok := cfg["defaultRegion"].(string); ok && strings.TrimSpace(v) != "" {
			merged.DefaultRegion = strings.TrimSpace(v)
		}
		return New(merged)
	}
}

// New constructs a Hetzner Provider.
func New(cfg Config) (*Provider, error) {
	spec := cfg.Spec
	if spec.NetworkCIDR == "" {
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
		if cfg.Spec.NetworkCIDR != "" {
			spec.NetworkCIDR = cfg.Spec.NetworkCIDR
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
	tokens := cfg.Tokens
	if tokens == nil {
		tokens = StaticToken("")
	}
	api := cfg.API
	if api == nil {
		api = NewHTTPClient(cfg.APIBase, tokens, lim, log)
	}
	if cfg.DefaultRegion == "" {
		cfg.DefaultRegion = "fsn1"
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
		name := l.City
		if name == "" {
			name = l.Name
		}
		out = append(out, provider.Region{ID: l.Name, Name: name})
	}
	return out, nil
}

func (p *Provider) ListMachineTypes(ctx context.Context, region string) ([]provider.MachineType, error) {
	types, err := p.api.ListServerTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]provider.MachineType, 0, len(types))
	for _, st := range types {
		out = append(out, provider.MachineType{
			ID:        st.Name,
			Name:      st.Name,
			CPU:       st.Cores,
			MemoryMiB: int(st.Memory * 1024),
			DiskGiB:   st.Disk,
			Region:    region,
		})
	}
	return out, nil
}

func (p *Provider) CreateNetwork(ctx context.Context, opID string, req provider.CreateNetworkRequest) (*provider.Network, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "forge-net-" + shortName(opID)
	}
	cidr := req.CIDR
	if cidr == "" {
		cidr = p.spec.NetworkCIDR
	}
	// Idempotent: adopt existing network with same op_id label.
	existing, err := p.api.ListNetworks(ctx, LabelSelectorOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		n := existing[0]
		return &provider.Network{ID: networkID(n.ID), Name: n.Name, Region: req.Region, CIDR: n.IPRange}, nil
	}
	zone := networkZone(req.Region)
	if zone == "" {
		zone = networkZone(p.cfg.DefaultRegion)
	}
	labels := map[string]string{
		LabelManaged:  LabelManagedValue,
		LabelOpID:     opID,
		LabelRole:     RoleNetwork,
		LabelNodePool: name,
	}
	created, err := p.api.CreateNetwork(ctx, CreateNetworkAPIRequest{
		Name:    name,
		IPRange: cidr,
		Labels:  labels,
		Subnets: []NetworkSubnet{{
			Type:        "cloud",
			NetworkZone: zone,
			IPRange:     cidr,
		}},
	})
	if err != nil {
		return nil, err
	}
	p.record("network.create")
	p.log.Info("hetzner network created",
		"event", "infra.provider.hetzner.create_network",
		"network_id", created.ID,
		"op_id", opID,
	)
	return &provider.Network{ID: networkID(created.ID), Name: created.Name, Region: req.Region, CIDR: created.IPRange}, nil
}

func (p *Provider) DeleteNetwork(ctx context.Context, opID string, netID string) error {
	id, err := parseResourceID(netID, "hetzner-net:")
	if err != nil {
		return err
	}
	if err := p.api.DeleteNetwork(ctx, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("network.delete")
	p.log.Info("hetzner network deleted",
		"event", "infra.provider.hetzner.delete_network",
		"network_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	p.log.Info("hetzner create_node start",
		"event", "infra.provider.hetzner.create_node",
		"op_id", opID,
		"node_pool", req.NodePool,
		"machine_type", req.MachineType,
	)

	unlock := p.lockOp(opID)
	defer unlock()

	if existing, err := p.findServerByOpID(ctx, opID); err != nil {
		return nil, err
	} else if existing != nil {
		p.record("server.adopt")
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
	labels := ManagedLabels(pool, opID)
	for k, v := range req.Labels {
		if _, reserved := labels[k]; !reserved {
			labels[k] = v
		}
	}
	labels["forge.machine_type"] = req.MachineType

	userData := req.UserData
	name := req.Name
	if name == "" {
		name = "forge-" + shortName(pool) + "-" + shortName(opID)
	}
	image := p.spec.Image
	if image == "" {
		image = defaultImage
	}

	created, err := p.api.CreateServer(ctx, CreateServerRequest{
		Name:       sanitizeName(name),
		ServerType: req.MachineType,
		Image:      image,
		Location:   region,
		UserData:   userData,
		Labels:     labels,
		PublicNet:  &CreatePublicNet{EnableIPv4: true, EnableIPv6: false},
	})
	if err != nil {
		// Race: another caller may have created; adopt by label.
		if existing, findErr := p.findServerByOpID(ctx, opID); findErr == nil && existing != nil {
			p.record("server.adopt")
			return existing, nil
		}
		return nil, err
	}
	p.record("server.create")
	node := serverToNode(created)
	p.log.Info("hetzner create_node ok",
		"event", "infra.provider.hetzner.create_node",
		"server_id", created.ID,
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
	id, err := parseNodeID(nodeID)
	if err != nil {
		return err
	}
	if err := p.api.RebootServer(ctx, id); err != nil {
		return err
	}
	p.record("server.reboot")
	p.log.Info("hetzner reboot_node ok",
		"event", "infra.provider.hetzner.reboot_node",
		"server_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) GetNode(ctx context.Context, nodeID string) (*provider.ProviderNode, error) {
	id, err := parseNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	srv, err := p.api.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	return serverToNode(srv), nil
}

func (p *Provider) ListNodes(ctx context.Context) ([]provider.ProviderNode, error) {
	servers, err := p.api.ListServers(ctx, LabelSelectorManaged())
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProviderNode, 0, len(servers))
	for i := range servers {
		out = append(out, *serverToNode(&servers[i]))
	}
	return out, nil
}

func (p *Provider) AttachDisk(ctx context.Context, opID string, nodeID string, req provider.AttachDiskRequest) (*provider.Disk, error) {
	serverID, err := parseNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	srv, err := p.api.GetServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	loc := p.cfg.DefaultRegion
	if srv.Datacenter != nil && srv.Datacenter.Location != nil {
		loc = srv.Datacenter.Location.Name
	}
	size := req.SizeGiB
	if size < 10 {
		size = 10
	}
	name := req.Name
	if name == "" {
		name = "forge-vol-" + shortName(opID)
	}
	pool := ""
	if srv.Labels != nil {
		pool = srv.Labels[LabelNodePool]
	}
	vol, err := p.api.CreateVolume(ctx, CreateVolumeRequest{
		Name:     sanitizeName(name),
		Size:     size,
		Location: loc,
		Server:   &serverID,
		Labels: map[string]string{
			LabelManaged:  LabelManagedValue,
			LabelOpID:     opID,
			LabelNodePool: pool,
			LabelRole:     RoleVolume,
		},
	})
	if err != nil {
		return nil, err
	}
	p.record("volume.create")
	// Create with server attaches; if not attached yet, attach explicitly.
	if vol.Server == nil {
		if err := p.api.AttachVolume(ctx, vol.ID, serverID); err != nil {
			return nil, err
		}
		p.record("volume.attach")
	} else {
		p.record("volume.attach")
	}
	return &provider.Disk{
		ID:       volumeID(vol.ID),
		NodeID:   nodeID,
		SizeGiB:  vol.Size,
		Attached: true,
	}, nil
}

func (p *Provider) DetachDisk(ctx context.Context, opID string, nodeID string, diskID string) error {
	_ = nodeID
	id, err := parseResourceID(diskID, "hetzner-vol:")
	if err != nil {
		return err
	}
	if err := p.api.DetachVolume(ctx, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("volume.detach")
	p.log.Info("hetzner detach_disk",
		"event", "infra.provider.hetzner.detach_disk",
		"volume_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) ResizeDisk(ctx context.Context, opID string, diskID string, newSizeGiB int) error {
	id, err := parseResourceID(diskID, "hetzner-vol:")
	if err != nil {
		return err
	}
	if err := p.api.ResizeVolume(ctx, id, newSizeGiB); err != nil {
		return err
	}
	p.record("volume.resize")
	p.log.Info("hetzner resize_disk",
		"event", "infra.provider.hetzner.resize_disk",
		"volume_id", id,
		"new_size_gib", newSizeGiB,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) CreatePublicIP(ctx context.Context, opID string, req provider.CreatePublicIPRequest) (*provider.PublicIP, error) {
	existing, err := p.api.ListFloatingIPs(ctx, LabelSelectorOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		ip := existing[0]
		return &provider.PublicIP{ID: floatingID(ip.ID), Address: ip.IP, NodeID: req.NodeID}, nil
	}
	region := req.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	var serverPtr *int64
	if req.NodeID != "" {
		if sid, err := parseNodeID(req.NodeID); err == nil {
			serverPtr = &sid
		}
	}
	name := req.Name
	if name == "" {
		name = "forge-ip-" + shortName(opID)
	}
	ip, err := p.api.CreateFloatingIP(ctx, CreateFloatingIPRequest{
		Type:         "ipv4",
		HomeLocation: region,
		Name:         sanitizeName(name),
		Server:       serverPtr,
		Labels: map[string]string{
			LabelManaged: LabelManagedValue,
			LabelOpID:    opID,
			LabelRole:    RoleFloatingIP,
		},
	})
	if err != nil {
		return nil, err
	}
	p.record("floating_ip.create")
	if serverPtr != nil && ip.Server == nil {
		if err := p.api.AssignFloatingIP(ctx, ip.ID, *serverPtr); err != nil {
			return nil, err
		}
		p.record("floating_ip.assign")
	}
	return &provider.PublicIP{ID: floatingID(ip.ID), Address: ip.IP, NodeID: req.NodeID}, nil
}

func (p *Provider) DeletePublicIP(ctx context.Context, opID string, ipID string) error {
	id, err := parseResourceID(ipID, "hetzner-ip:")
	if err != nil {
		return err
	}
	_ = p.api.UnassignFloatingIP(ctx, id)
	p.record("floating_ip.unassign")
	if err := p.api.DeleteFloatingIP(ctx, id); err != nil && !IsNotFound(err) {
		return err
	}
	p.record("floating_ip.delete")
	p.log.Info("hetzner delete_public_ip",
		"event", "infra.provider.hetzner.delete_public_ip",
		"floating_ip_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) GetPricing(ctx context.Context, region string, machineType string) (*provider.Pricing, error) {
	types, err := p.api.ListServerTypes(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range types {
		if !strings.EqualFold(st.Name, machineType) {
			continue
		}
		hourly := 0.0
		for _, pr := range st.Prices {
			if region == "" || strings.EqualFold(pr.Location, region) {
				if v, err := strconv.ParseFloat(pr.PriceHourly.Gross, 64); err == nil {
					hourly = v
					break
				}
			}
		}
		return &provider.Pricing{
			Region:      region,
			MachineType: machineType,
			HourlyUSD:   hourly,
			Currency:    "EUR",
		}, nil
	}
	return nil, fmt.Errorf("unknown machine type %q", machineType)
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

func (p *Provider) findServerByOpID(ctx context.Context, opID string) (*provider.ProviderNode, error) {
	servers, err := p.api.ListServers(ctx, LabelSelectorOpID(opID))
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, nil
	}
	return serverToNode(&servers[0]), nil
}

func serverToNode(s *Server) *provider.ProviderNode {
	addr := ""
	if s.PublicNet != nil && s.PublicNet.IPv4 != nil {
		addr = s.PublicNet.IPv4.IP
	}
	region := ""
	if s.Datacenter != nil && s.Datacenter.Location != nil {
		region = s.Datacenter.Location.Name
	}
	mt := ""
	if s.ServerType != nil {
		mt = s.ServerType.Name
	}
	if mt == "" && s.Labels != nil {
		mt = s.Labels["forge.machine_type"]
	}
	phase := "Provisioning"
	switch strings.ToLower(s.Status) {
	case "running":
		phase = "Ready"
	case "off", "stopping":
		phase = "Stopped"
	case "deleting":
		phase = "Deleting"
	case "initializing", "starting":
		phase = "Provisioning"
	}
	labels := s.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	return &provider.ProviderNode{
		ID:          nodeIDPrefix + strconv.FormatInt(s.ID, 10),
		Name:        s.Name,
		Region:      region,
		MachineType: mt,
		Address:     addr,
		Phase:       phase,
		Labels:      labels,
	}
}

func parseNodeID(id string) (int64, error) {
	return parseResourceID(id, nodeIDPrefix)
}

func parseResourceID(id, prefix string) (int64, error) {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, prefix)
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid resource id %q", id)
	}
	return n, nil
}

func networkID(id int64) string  { return "hetzner-net:" + strconv.FormatInt(id, 10) }
func volumeID(id int64) string   { return "hetzner-vol:" + strconv.FormatInt(id, 10) }
func floatingID(id int64) string { return "hetzner-ip:" + strconv.FormatInt(id, 10) }

func networkZone(region string) string {
	// Hetzner locations map to eu-central / us-east / us-west network zones.
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "fsn1", "nbg1", "hel1":
		return "eu-central"
	case "ash":
		return "us-east"
	case "hil":
		return "us-west"
	default:
		if region == "" {
			return "eu-central"
		}
		return "eu-central"
	}
}

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

// Ensure compile-time Provider conformance.
var _ provider.Provider = (*Provider)(nil)
