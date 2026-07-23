package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"forge.local/services/forge-infrastructure/internal/provider"
)

const (
	nodeIDPrefix = "docker:"
	volSuffix    = "-data"
)

// Config holds Docker provider runtime settings.
type Config struct {
	Socket      string // unix path or DOCKER_HOST URL
	Network     string
	Image       string
	HostAddress string
	ControlURL  string
	StopGraceS  int
	Log         *slog.Logger
}

// Provider implements provider.Provider against the Docker Engine API.
type Provider struct {
	cfg    Config
	engine Engine
	log    *slog.Logger

	orphansRemoved atomic.Int64
}

// Factory returns a ProviderFactory that builds Providers from env defaults + spec config.
func Factory(defaults Config) provider.ProviderFactory {
	return func(cfg map[string]any) (provider.Provider, error) {
		merged := defaults
		if v, ok := cfg["socket"].(string); ok && strings.TrimSpace(v) != "" {
			merged.Socket = v
		}
		if v, ok := cfg["network"].(string); ok && strings.TrimSpace(v) != "" {
			merged.Network = v
		}
		if v, ok := cfg["image"].(string); ok && strings.TrimSpace(v) != "" {
			merged.Image = v
		}
		if v, ok := cfg["hostAddress"].(string); ok && strings.TrimSpace(v) != "" {
			merged.HostAddress = v
		}
		if v, ok := cfg["controlURL"].(string); ok && strings.TrimSpace(v) != "" {
			merged.ControlURL = v
		}
		p, err := New(merged)
		if err != nil {
			return nil, err
		}
		return p, nil
	}
}

// New constructs a Provider with a real Engine client.
func New(cfg Config) (*Provider, error) {
	cfg = normalizeConfig(cfg)
	eng, err := NewClient(cfg.Socket)
	if err != nil {
		return nil, err
	}
	return NewWithEngine(cfg, eng), nil
}

// NewWithEngine constructs a Provider with an injected Engine (tests).
func NewWithEngine(cfg Config, eng Engine) *Provider {
	cfg = normalizeConfig(cfg)
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &Provider{cfg: cfg, engine: eng, log: log}
}

func normalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.Socket) == "" {
		cfg.Socket = "/var/run/docker.sock"
	}
	if strings.TrimSpace(cfg.Network) == "" {
		cfg.Network = "forge-platform_default"
	}
	if strings.TrimSpace(cfg.Image) == "" {
		cfg.Image = "forge/forge-runtime:local"
	}
	if strings.TrimSpace(cfg.HostAddress) == "" {
		cfg.HostAddress = "127.0.0.1"
	}
	if strings.TrimSpace(cfg.ControlURL) == "" {
		cfg.ControlURL = "http://forge-control:8080"
	}
	if cfg.StopGraceS <= 0 {
		cfg.StopGraceS = 10
	}
	return cfg
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	return p.engine.Ping(ctx)
}

func (p *Provider) ListRegions(ctx context.Context) ([]provider.Region, error) {
	return []provider.Region{{ID: "local", Name: "local"}}, nil
}

func (p *Provider) ListMachineTypes(ctx context.Context, region string) ([]provider.MachineType, error) {
	if region == "" {
		region = "local"
	}
	return AllMachineTypes(region), nil
}

func (p *Provider) CreateNetwork(ctx context.Context, opID string, req provider.CreateNetworkRequest) (*provider.Network, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || name == p.cfg.Network {
		// Default: reuse the Compose network (no public-IP concept locally for networks either).
		ni, err := p.engine.NetworkInspect(ctx, p.cfg.Network)
		if err != nil {
			return &provider.Network{ID: "docker-net:" + p.cfg.Network, Name: p.cfg.Network, Region: req.Region, CIDR: req.CIDR}, nil
		}
		return &provider.Network{ID: "docker-net:" + ni.ID, Name: ni.Name, Region: req.Region, CIDR: req.CIDR}, nil
	}
	labels := ManagedLabels(name, opID)
	id, err := p.engine.NetworkCreate(ctx, name, labels)
	if err != nil {
		return nil, err
	}
	p.log.Info("docker network created",
		"event", "infra.provider.docker.create_network",
		"network_id", id,
		"op_id", opID,
	)
	return &provider.Network{ID: "docker-net:" + id, Name: name, Region: req.Region, CIDR: req.CIDR}, nil
}

func (p *Provider) DeleteNetwork(ctx context.Context, opID string, networkID string) error {
	id := strings.TrimPrefix(networkID, "docker-net:")
	if id == "" || id == p.cfg.Network {
		return nil // never delete the shared Compose network
	}
	ni, err := p.engine.NetworkInspect(ctx, id)
	if err == nil && (ni.Name == p.cfg.Network || ni.ID == p.cfg.Network) {
		return nil
	}
	if err := p.engine.NetworkRemove(ctx, id); err != nil {
		return err
	}
	p.log.Info("docker network deleted",
		"event", "infra.provider.docker.delete_network",
		"network_id", id,
		"op_id", opID,
	)
	return nil
}

func (p *Provider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	p.log.Info("docker create_node start",
		"event", "infra.provider.docker.create_node",
		"op_id", opID,
		"node_pool", req.NodePool,
		"machine_type", req.MachineType,
	)

	// Idempotency: same op_id never produces two containers.
	if existing, err := p.findByOpID(ctx, opID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	mt, err := LookupMachineType(req.MachineType)
	if err != nil {
		return nil, err
	}
	pool := req.NodePool
	if pool == "" {
		pool = req.Name
	}
	suffix := shortID()
	name := fmt.Sprintf("forge-node-%s-%s", sanitizeName(pool), suffix)
	volName := name + volSuffix
	labels := ManagedLabels(pool, opID)
	for k, v := range req.Labels {
		if _, reserved := labels[k]; !reserved {
			labels[k] = v
		}
	}
	labels["forge.machine_type"] = mt.ID

	if _, err := p.engine.VolumeCreate(ctx, volName, labels); err != nil {
		return nil, fmt.Errorf("volume create: %w", err)
	}

	controlURL := p.cfg.ControlURL
	if req.Env != nil {
		if v := strings.TrimSpace(req.Env["FORGE_CONTROL_URL"]); v != "" {
			controlURL = v
		}
	}
	nodeAddress := "http://" + name + ":8080"
	env := []string{
		"PORT=8080",
		"FORGE_AUTH_MODE=dev",
		"FORGE_CONTROL_URL=" + controlURL,
		"FORGE_RUNTIME_DATA_DIR=/var/lib/forge-runtime",
		"FORGE_NODE_SLOTS=" + strconv.Itoa(mt.Slots),
		"FORGE_NODE_ADDRESS=" + nodeAddress,
		"FORGE_SERVICE_NAME=forge-runtime",
		"FORGE_LIFECYCLE_OWNER=control",
		"DOCKER_HOST=unix:///var/run/docker.sock",
		"FORGE_NODE_DOCKER_COLOCATED=true",
		"FORGE_HEARTBEAT_INTERVAL_MS=2000",
		"FORGE_NETWORK_WG_BACKEND=fake",
		"FORGE_NETWORK_ROUTE_BACKEND=fake",
		"FORGE_NETWORK_POLICY_BACKEND=fake",
		"FORGE_NETWORK_DNS_BACKEND=fake",
	}
	if p.cfg.ControlURL != "" {
		// Overlay join uses the same Control URL host network namespace as compose.
		env = append(env, "FORGE_NETWORK_URL=http://forge-network:8080")
		env = append(env, "FORGE_NETWORK_NAME=cluster-overlay")
	}
	// Optional Discovery registration for provider-created agents (M1 HA demo).
	// Inherited from forge-infrastructure process env when set; req.Env wins.
	for _, key := range []string{
		"FORGE_DISCOVERY_URL",
		"FORGE_DISCOVERY_REGISTER_ENABLED",
		"FORGE_DISCOVERY_LEASE_SECONDS",
		"FORGE_DISCOVERY_DEFAULT_PROJECT",
		"FORGE_DISCOVERY_DEFAULT_ENVIRONMENT",
	} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			if req.Env == nil || strings.TrimSpace(req.Env[key]) == "" {
				env = append(env, key+"="+v)
			}
		}
	}
	if tok := strings.TrimSpace(req.BootstrapToken); tok != "" {
		env = append(env, "FORGE_NODE_BOOTSTRAP_TOKEN="+tok)
	} else if req.Env != nil {
		if tok := strings.TrimSpace(req.Env["FORGE_NODE_BOOTSTRAP_TOKEN"]); tok != "" {
			env = append(env, "FORGE_NODE_BOOTSTRAP_TOKEN="+tok)
		}
	}
	for k, v := range req.Env {
		if k == "FORGE_CONTROL_URL" || k == "FORGE_NODE_BOOTSTRAP_TOKEN" {
			continue
		}
		if strings.TrimSpace(k) == "" {
			continue
		}
		env = append(env, k+"="+v)
	}

	containerID, err := p.engine.ContainerCreate(ctx, name, ContainerConfig{
		Image:   p.cfg.Image,
		Env:     env,
		Labels:  labels,
		Network: p.cfg.Network,
		// Socket mount + root user match compose forge-runtime — required so
		// scheduled workloads can create containers on the host Docker engine
		// and /health/ready can leave the docker-unreachable 503 state.
		Binds: []string{
			volName + ":/var/lib/forge-runtime",
			"/var/run/docker.sock:/var/run/docker.sock",
		},
		User:        "0:0",
		GroupAdd:    []string{"0"},
		NanoCPUs:    int64(mt.CPU) * 1_000_000_000,
		MemoryBytes: int64(mt.MemoryMiB) * 1024 * 1024,
	})
	if err != nil {
		_ = p.engine.VolumeRemove(ctx, volName, true)
		return nil, fmt.Errorf("container create: %w", err)
	}

	if err := p.engine.ContainerStart(ctx, containerID); err != nil {
		_ = p.engine.ContainerRemove(ctx, containerID, true)
		_ = p.engine.VolumeRemove(ctx, volName, true)
		return nil, fmt.Errorf("container start: %w", err)
	}

	node, err := p.providerNodeFromID(ctx, containerID, mt.ID)
	if err != nil {
		return nil, err
	}
	// Prefer Compose DNS name so JoinObserver matches fleet FORGE_NODE_ADDRESS.
	node.Address = nodeAddress
	node.Name = name
	p.log.Info("docker create_node ok",
		"event", "infra.provider.docker.create_node",
		"container_id", shortContainerID(containerID),
		"node_pool", pool,
		"action", "create",
		"op_id", opID,
	)
	return node, nil
}

func (p *Provider) DeleteNode(ctx context.Context, opID string, nodeID string) error {
	id := stripNodePrefix(nodeID)
	if id == "" {
		return nil
	}
	insp, err := p.engine.ContainerInspect(ctx, id)
	volName := ""
	if err == nil && insp.Name != "" {
		volName = insp.Name + volSuffix
	}
	_ = p.engine.ContainerStop(ctx, id, p.cfg.StopGraceS)
	if rmErr := p.engine.ContainerRemove(ctx, id, true); rmErr != nil {
		return rmErr
	}
	if volName != "" {
		_ = p.engine.VolumeRemove(ctx, volName, true)
	}
	p.log.Info("docker delete_node ok",
		"event", "infra.provider.docker.delete_node",
		"container_id", shortContainerID(id),
		"action", "delete",
		"op_id", opID,
	)
	return nil
}

func (p *Provider) RebootNode(ctx context.Context, opID string, nodeID string) error {
	id := stripNodePrefix(nodeID)
	if err := p.engine.ContainerRestart(ctx, id, p.cfg.StopGraceS); err != nil {
		return err
	}
	p.log.Info("docker reboot_node ok",
		"event", "infra.provider.docker.reboot_node",
		"container_id", shortContainerID(id),
		"action", "reboot",
		"op_id", opID,
	)
	return nil
}

func (p *Provider) GetNode(ctx context.Context, nodeID string) (*provider.ProviderNode, error) {
	id := stripNodePrefix(nodeID)
	insp, err := p.engine.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	mt := ""
	if insp.Config.Labels != nil {
		mt = insp.Config.Labels["forge.machine_type"]
	}
	return inspectToNode(insp, mt, p.cfg.Network), nil
}

func (p *Provider) ListNodes(ctx context.Context) ([]provider.ProviderNode, error) {
	list, err := p.engine.ContainerList(ctx, map[string][]string{
		"label": {LabelManaged + "=" + LabelManagedValue},
	}, true)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProviderNode, 0, len(list))
	for _, c := range list {
		mt := ""
		if c.Labels != nil {
			mt = c.Labels["forge.machine_type"]
		}
		phase := "Provisioning"
		if strings.EqualFold(c.State, "running") {
			phase = "Ready"
		} else if c.State != "" {
			phase = "Stopped"
		}
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
		}
		out = append(out, provider.ProviderNode{
			ID:          nodeIDPrefix + c.ID,
			Name:        name,
			MachineType: mt,
			Phase:       phase,
			Labels:      c.Labels,
			Region:      "local",
		})
	}
	return out, nil
}

func (p *Provider) AttachDisk(ctx context.Context, opID string, nodeID string, req provider.AttachDiskRequest) (*provider.Disk, error) {
	name := req.Name
	if name == "" {
		name = "forge-disk-" + shortID()
	}
	labels := map[string]string{
		LabelManaged: LabelManagedValue,
		LabelOpID:    opID,
		LabelSizeGiB: strconv.Itoa(req.SizeGiB),
	}
	vol, err := p.engine.VolumeCreate(ctx, name, labels)
	if err != nil {
		return nil, err
	}
	return &provider.Disk{
		ID:       "docker-vol:" + vol,
		NodeID:   nodeID,
		SizeGiB:  req.SizeGiB,
		Attached: true,
	}, nil
}

func (p *Provider) DetachDisk(ctx context.Context, opID string, nodeID string, diskID string) error {
	_ = nodeID
	_ = opID
	name := strings.TrimPrefix(diskID, "docker-vol:")
	return p.engine.VolumeRemove(ctx, name, true)
}

func (p *Provider) ResizeDisk(ctx context.Context, opID string, diskID string, newSizeGiB int) error {
	// Docker does not enforce volume quotas; documented no-op with size annotation intent.
	_ = opID
	_ = diskID
	_ = newSizeGiB
	p.log.Info("docker resize_disk noop",
		"event", "infra.provider.docker.resize_disk",
		"disk_id", diskID,
		"new_size_gib", newSizeGiB,
		"note", "Docker volumes have no quota enforcement; size is annotation-only",
		"op_id", opID,
	)
	return nil
}

func (p *Provider) CreatePublicIP(ctx context.Context, opID string, req provider.CreatePublicIPRequest) (*provider.PublicIP, error) {
	// No public IP concept locally — return the configured host bind address.
	_ = opID
	id := "docker-ip:local"
	if req.Name != "" {
		id = "docker-ip:" + req.Name
	}
	return &provider.PublicIP{
		ID:      id,
		Address: p.cfg.HostAddress,
		NodeID:  req.NodeID,
	}, nil
}

func (p *Provider) DeletePublicIP(ctx context.Context, opID string, ipID string) error {
	_ = opID
	_ = ipID
	return nil
}

func (p *Provider) GetPricing(ctx context.Context, region string, machineType string) (*provider.Pricing, error) {
	return &provider.Pricing{
		Region:      region,
		MachineType: machineType,
		HourlyUSD:   0,
		Currency:    "USD",
	}, nil
}

// OrphansRemoved returns how many orphan containers this provider has deleted.
func (p *Provider) OrphansRemoved() int64 { return p.orphansRemoved.Load() }

func (p *Provider) findByOpID(ctx context.Context, opID string) (*provider.ProviderNode, error) {
	list, err := p.engine.ContainerList(ctx, map[string][]string{
		"label": {
			LabelManaged + "=" + LabelManagedValue,
			LabelOpID + "=" + opID,
		},
	}, true)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	mt := ""
	if list[0].Labels != nil {
		mt = list[0].Labels["forge.machine_type"]
	}
	return p.providerNodeFromID(ctx, list[0].ID, mt)
}

func (p *Provider) providerNodeFromID(ctx context.Context, containerID, machineType string) (*provider.ProviderNode, error) {
	insp, err := p.engine.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, err
	}
	return inspectToNode(insp, machineType, p.cfg.Network), nil
}

func inspectToNode(insp *ContainerInspect, machineType, network string) *provider.ProviderNode {
	addr := insp.NetworkSettings.IPAddress
	if addr == "" && insp.NetworkSettings.Networks != nil {
		if ep, ok := insp.NetworkSettings.Networks[network]; ok {
			addr = ep.IPAddress
		} else {
			for _, ep := range insp.NetworkSettings.Networks {
				if ep.IPAddress != "" {
					addr = ep.IPAddress
					break
				}
			}
		}
	}
	phase := "Provisioning"
	if insp.State.Running {
		phase = "Ready"
	} else if insp.State.Status != "" {
		phase = "Stopped"
	}
	labels := insp.Config.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	if machineType == "" {
		machineType = labels["forge.machine_type"]
	}
	return &provider.ProviderNode{
		ID:          nodeIDPrefix + insp.ID,
		Name:        insp.Name,
		Region:      "local",
		MachineType: machineType,
		Address:     addr,
		Phase:       phase,
		Labels:      labels,
	}
}

func stripNodePrefix(id string) string {
	return strings.TrimPrefix(id, nodeIDPrefix)
}

func shortContainerID(id string) string {
	id = stripNodePrefix(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func shortID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "pool"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// Ensure compile-time Provider conformance.
var _ provider.Provider = (*Provider)(nil)
