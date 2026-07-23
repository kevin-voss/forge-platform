package ssh

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"forge.local/services/forge-infrastructure/internal/bootstrap"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/inventory"
	"forge.local/services/forge-infrastructure/internal/provider/sshprobe"
)

const defaultRegion = "on-prem"

// Config holds SSH / bare-metal adopt-provider settings.
type Config struct {
	ProviderName   string // InfrastructureProvider.metadata.name
	TypeName       string // "ssh" or "bare-metal"
	IDPrefix       string // "ssh" or "bare-metal"
	Inventory      []inventory.Host
	ConnectTimeout time.Duration
	Store          inventory.Store
	Prober         sshprobe.Prober
	Secrets        SecretResolver
	Log            *slog.Logger
	RuntimeImage   string
	ControlURL     string
}

// Provider adopts/releases existing hosts from a static inventory.
type Provider struct {
	cfg         Config
	store       inventory.Store
	prober      sshprobe.Prober
	secrets     SecretResolver
	log         *slog.Logger
	mu          sync.Mutex
	unreachable map[string]bool
	capCache    map[string]sshprobe.Capacity
}

// Factory returns a ProviderFactory for type "ssh".
func Factory(defaults Config) provider.ProviderFactory {
	defaults.TypeName = provider.TypeSSH
	defaults.IDPrefix = "ssh"
	return factoryWithDefaults(defaults)
}

// BareMetalFactory returns a ProviderFactory for type "bare-metal".
func BareMetalFactory(defaults Config) provider.ProviderFactory {
	defaults.TypeName = provider.TypeBareMetal
	defaults.IDPrefix = "bare-metal"
	return factoryWithDefaults(defaults)
}

func factoryWithDefaults(defaults Config) provider.ProviderFactory {
	return func(cfg map[string]any) (provider.Provider, error) {
		merged := defaults
		hosts, err := inventory.ParseConfig(cfg)
		if err != nil {
			return nil, err
		}
		merged.Inventory = hosts
		if v, ok := cfg["providerName"].(string); ok && strings.TrimSpace(v) != "" {
			merged.ProviderName = v
		}
		return New(merged)
	}
}

// New constructs an adopt/release Provider.
func New(cfg Config) (*Provider, error) {
	if cfg.TypeName == "" {
		cfg.TypeName = provider.TypeSSH
	}
	if cfg.IDPrefix == "" {
		cfg.IDPrefix = "ssh"
	}
	if cfg.ProviderName == "" {
		cfg.ProviderName = "default"
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.Store == nil {
		cfg.Store = inventory.NewMemoryStore()
	}
	if cfg.Prober == nil {
		cfg.Prober = sshprobe.New(sshprobe.Config{ConnectTimeout: cfg.ConnectTimeout})
	}
	if cfg.Secrets == nil {
		cfg.Secrets = &MapSecrets{Keys: map[string][]byte{}}
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	p := &Provider{
		cfg:         cfg,
		store:       cfg.Store,
		prober:      cfg.Prober,
		secrets:     cfg.Secrets,
		log:         log,
		unreachable: map[string]bool{},
		capCache:    map[string]sshprobe.Capacity{},
	}
	addrs := make([]string, 0, len(cfg.Inventory))
	for _, h := range cfg.Inventory {
		addrs = append(addrs, h.Address)
	}
	if err := p.store.EnsureHosts(context.Background(), cfg.ProviderName, addrs); err != nil {
		return nil, err
	}
	p.log.Info("inventory provider ready",
		"provider_name", cfg.ProviderName,
		"type", cfg.TypeName,
		"metric", "forge_infra_inventory_capacity",
		"capacity", len(cfg.Inventory),
	)
	return p, nil
}

// MaxReplicas implements provider.InventoryCapacitor.
func (p *Provider) MaxReplicas() int {
	return len(p.cfg.Inventory)
}

func (p *Provider) hostByAddress(addr string) (inventory.Host, bool) {
	for _, h := range p.cfg.Inventory {
		if strings.EqualFold(h.Address, addr) {
			return h, true
		}
	}
	return inventory.Host{}, false
}

func (p *Provider) nodeID(address string) string {
	return p.cfg.IDPrefix + ":" + address
}

func (p *Provider) addressFromNodeID(nodeID string) string {
	prefix := p.cfg.IDPrefix + ":"
	if strings.HasPrefix(nodeID, prefix) {
		return strings.TrimPrefix(nodeID, prefix)
	}
	// tolerate alternate prefixes for release across type renames
	for _, pfx := range []string{"ssh:", "bare-metal:"} {
		if strings.HasPrefix(nodeID, pfx) {
			return strings.TrimPrefix(nodeID, pfx)
		}
	}
	return nodeID
}

func (p *Provider) resolveKey(ctx context.Context, h inventory.Host) ([]byte, error) {
	return p.secrets.ResolveSSHKey(ctx, h.KeySecretName)
}

func (p *Provider) reachableCandidates() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.cfg.Inventory))
	for _, h := range p.cfg.Inventory {
		if p.unreachable[h.Address] {
			continue
		}
		out = append(out, h.Address)
	}
	return out
}

func (p *Provider) markUnreachable(addr string, unreachable bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if unreachable {
		p.unreachable[addr] = true
	} else {
		delete(p.unreachable, addr)
	}
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	reachable := 0
	total := len(p.cfg.Inventory)
	var firstErr error
	for _, h := range p.cfg.Inventory {
		key, err := p.resolveKey(ctx, h)
		if err != nil {
			p.markUnreachable(h.Address, true)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		cap, err := p.prober.ProbeCapacity(ctx, h.Address, h.SSHUser, key)
		if err != nil {
			p.markUnreachable(h.Address, true)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		p.markUnreachable(h.Address, false)
		p.mu.Lock()
		p.capCache[h.Address] = cap
		p.mu.Unlock()
		reachable++
	}
	p.log.Info("inventory credentials sweep",
		"provider_name", p.cfg.ProviderName,
		"reachable", reachable,
		"total", total,
	)
	if reachable == 0 && total > 0 {
		if firstErr != nil {
			return firstErr
		}
		return fmt.Errorf("0/%d hosts reachable", total)
	}
	// Partial availability is OK — not a hard failure.
	return nil
}

func (p *Provider) ListRegions(ctx context.Context) ([]provider.Region, error) {
	_ = ctx
	return []provider.Region{{ID: defaultRegion, Name: defaultRegion}}, nil
}

func (p *Provider) ListMachineTypes(ctx context.Context, region string) ([]provider.MachineType, error) {
	_ = region
	out := make([]provider.MachineType, 0, len(p.cfg.Inventory))
	for _, h := range p.cfg.Inventory {
		cap := sshprobe.Capacity{CPU: 1, MemoryMiB: 1024}
		p.mu.Lock()
		if c, ok := p.capCache[h.Address]; ok {
			cap = c
		}
		p.mu.Unlock()
		if _, ok := p.capCache[h.Address]; !ok {
			key, err := p.resolveKey(ctx, h)
			if err == nil {
				if c, err := p.prober.ProbeCapacity(ctx, h.Address, h.SSHUser, key); err == nil {
					cap = c
					p.mu.Lock()
					p.capCache[h.Address] = c
					p.mu.Unlock()
				}
			}
		}
		out = append(out, provider.MachineType{
			ID:        "host-" + h.Address,
			Name:      "host-" + h.Address,
			CPU:       cap.CPU,
			MemoryMiB: cap.MemoryMiB,
			Region:    defaultRegion,
		})
	}
	return out, nil
}

func (p *Provider) CreateNetwork(ctx context.Context, opID string, req provider.CreateNetworkRequest) (*provider.Network, error) {
	_ = ctx
	_ = opID
	_ = req
	return nil, provider.ErrNotSupported
}

func (p *Provider) DeleteNetwork(ctx context.Context, opID string, networkID string) error {
	_ = ctx
	_ = opID
	_ = networkID
	return provider.ErrNotSupported
}

func (p *Provider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	span := "infra.provider." + p.cfg.TypeName + ".create_node"
	p.log.Info("create_node start",
		"event", span,
		"op_id", opID,
		"node_pool", req.NodePool,
		"provider_name", p.cfg.ProviderName,
	)

	pool := req.NodePool
	if pool == "" {
		pool = req.Name
	}
	candidates := p.reachableCandidates()
	addr, err := p.store.ClaimNext(ctx, p.cfg.ProviderName, pool, candidates)
	if err != nil {
		if err == inventory.ErrNoFreeHost {
			return nil, fmt.Errorf("%w: provider %s inventory size=%d", provider.ErrInventoryExhausted, p.cfg.ProviderName, len(p.cfg.Inventory))
		}
		return nil, err
	}

	h, ok := p.hostByAddress(addr)
	if !ok {
		_ = p.store.Release(ctx, p.cfg.ProviderName, addr)
		return nil, fmt.Errorf("claimed address %q missing from inventory", addr)
	}
	key, err := p.resolveKey(ctx, h)
	if err != nil {
		_ = p.store.Release(ctx, p.cfg.ProviderName, addr)
		return nil, err
	}

	cap, err := p.prober.ProbeCapacity(ctx, h.Address, h.SSHUser, key)
	if err != nil {
		p.markUnreachable(h.Address, true)
		_ = p.store.Release(ctx, p.cfg.ProviderName, addr)
		return nil, err
	}
	p.mu.Lock()
	p.capCache[h.Address] = cap
	p.mu.Unlock()

	payload := bootstrap.Payload{
		ControlURL:     p.cfg.ControlURL,
		BootstrapToken: req.BootstrapToken,
		NodePool:       pool,
		RuntimeImage:   p.cfg.RuntimeImage,
	}
	if payload.ControlURL == "" {
		payload.ControlURL = "http://forge-control:8080"
	}
	if payload.RuntimeImage == "" {
		payload.RuntimeImage = "forge/forge-runtime:local"
	}
	script := req.UserData
	if strings.TrimSpace(script) == "" {
		script = bootstrap.RenderSSHScript(payload)
	}
	if _, err := p.prober.Run(ctx, h.Address, h.SSHUser, key, script); err != nil {
		_ = p.store.Release(ctx, p.cfg.ProviderName, addr)
		return nil, fmt.Errorf("ssh bootstrap %s: %w", h.Address, err)
	}

	claimed, _ := p.store.CountClaimed(ctx, p.cfg.ProviderName)
	p.log.Info("inventory claim",
		"provider_name", p.cfg.ProviderName,
		"address", h.Address,
		"node_pool", pool,
		"action", "claim",
		"metric", "forge_infra_inventory_claimed",
		"claimed", claimed,
	)

	return &provider.ProviderNode{
		ID:          p.nodeID(h.Address),
		Name:        req.Name,
		Region:      defaultRegion,
		MachineType: "host-" + h.Address,
		Address:     h.Address,
		Phase:       "Provisioning",
		Labels: map[string]string{
			"forge.local/node-pool": pool,
			"forge.inventory":       "true",
			"forge.provider_type":   p.cfg.TypeName,
		},
	}, nil
}

func (p *Provider) DeleteNode(ctx context.Context, opID string, nodeID string) error {
	addr := p.addressFromNodeID(nodeID)
	h, ok := p.hostByAddress(addr)
	if !ok {
		// Still release if claim row exists.
		_ = p.store.Release(ctx, p.cfg.ProviderName, addr)
		return nil
	}
	key, err := p.resolveKey(ctx, h)
	if err != nil {
		return err
	}
	uninstall := `#!/bin/sh
set -eu
docker rm -f forge-runtime >/dev/null 2>&1 || true
rm -f /etc/forge/runtime.env
`
	if _, err := p.prober.Run(ctx, h.Address, h.SSHUser, key, uninstall); err != nil {
		p.log.Warn("ssh uninstall failed; releasing claim anyway",
			"provider_name", p.cfg.ProviderName,
			"address", h.Address,
			"op_id", opID,
			"error", err.Error(),
		)
	}
	if err := p.store.Release(ctx, p.cfg.ProviderName, addr); err != nil {
		return err
	}
	p.log.Info("inventory release",
		"provider_name", p.cfg.ProviderName,
		"address", h.Address,
		"node_pool", "",
		"action", "release",
		"op_id", opID,
	)
	return nil
}

func (p *Provider) RebootNode(ctx context.Context, opID string, nodeID string) error {
	addr := p.addressFromNodeID(nodeID)
	h, ok := p.hostByAddress(addr)
	if !ok {
		return fmt.Errorf("unknown node %q", nodeID)
	}
	key, err := p.resolveKey(ctx, h)
	if err != nil {
		return err
	}
	_, err = p.prober.Run(ctx, h.Address, h.SSHUser, key, "sudo reboot || reboot || true")
	_ = opID
	return err
}

func (p *Provider) GetNode(ctx context.Context, nodeID string) (*provider.ProviderNode, error) {
	addr := p.addressFromNodeID(nodeID)
	claim, err := p.store.Get(ctx, p.cfg.ProviderName, addr)
	if err != nil {
		return nil, err
	}
	if claim == nil || claim.ClaimedByPool == "" {
		return nil, nil
	}
	h, _ := p.hostByAddress(addr)
	return &provider.ProviderNode{
		ID:      p.nodeID(addr),
		Address: addr,
		Region:  defaultRegion,
		Phase:   "Ready",
		Name:    claim.ClaimedByPool,
		Labels:  map[string]string{"forge.local/node-pool": claim.ClaimedByPool, "forge.ssh_user": h.SSHUser},
	}, nil
}

func (p *Provider) ListNodes(ctx context.Context) ([]provider.ProviderNode, error) {
	claims, err := p.store.List(ctx, p.cfg.ProviderName)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProviderNode, 0)
	for _, c := range claims {
		if c.ClaimedByPool == "" {
			continue
		}
		out = append(out, provider.ProviderNode{
			ID:      p.nodeID(c.Address),
			Address: c.Address,
			Region:  defaultRegion,
			Phase:   "Ready",
			Name:    c.ClaimedByPool,
		})
	}
	return out, nil
}

func (p *Provider) AttachDisk(ctx context.Context, opID string, nodeID string, req provider.AttachDiskRequest) (*provider.Disk, error) {
	_ = ctx
	_ = opID
	_ = nodeID
	_ = req
	return nil, provider.ErrNotSupported
}

func (p *Provider) DetachDisk(ctx context.Context, opID string, nodeID string, diskID string) error {
	_ = ctx
	_ = opID
	_ = nodeID
	_ = diskID
	return provider.ErrNotSupported
}

func (p *Provider) ResizeDisk(ctx context.Context, opID string, diskID string, newSizeGiB int) error {
	_ = ctx
	_ = opID
	_ = diskID
	_ = newSizeGiB
	return provider.ErrNotSupported
}

func (p *Provider) CreatePublicIP(ctx context.Context, opID string, req provider.CreatePublicIPRequest) (*provider.PublicIP, error) {
	_ = ctx
	_ = opID
	_ = req
	return nil, provider.ErrNotSupported
}

func (p *Provider) DeletePublicIP(ctx context.Context, opID string, ipID string) error {
	_ = ctx
	_ = opID
	_ = ipID
	return provider.ErrNotSupported
}

func (p *Provider) GetPricing(ctx context.Context, region string, machineType string) (*provider.Pricing, error) {
	_ = ctx
	return &provider.Pricing{
		Region:      region,
		MachineType: machineType,
		HourlyUSD:   0,
		Currency:    "USD",
	}, nil
}
