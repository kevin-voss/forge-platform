package noop

import (
	"context"

	"forge.local/services/forge-infrastructure/internal/provider"
)

// Provider is the default adapter until 23.02+ registers a real one.
// Mutating calls return ErrProviderNotConfigured; reads return empty slices/nil.
type Provider struct{}

// Factory returns a no-op ProviderFactory for the registry fallback.
func Factory(_ map[string]any) (provider.Provider, error) {
	return &Provider{}, nil
}

func (p *Provider) ValidateCredentials(ctx context.Context) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) ListRegions(ctx context.Context) ([]provider.Region, error) {
	return []provider.Region{}, nil
}

func (p *Provider) ListMachineTypes(ctx context.Context, region string) ([]provider.MachineType, error) {
	return []provider.MachineType{}, nil
}

func (p *Provider) CreateNetwork(ctx context.Context, opID string, req provider.CreateNetworkRequest) (*provider.Network, error) {
	return nil, provider.ErrProviderNotConfigured
}

func (p *Provider) DeleteNetwork(ctx context.Context, opID string, networkID string) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	return nil, provider.ErrProviderNotConfigured
}

func (p *Provider) DeleteNode(ctx context.Context, opID string, nodeID string) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) RebootNode(ctx context.Context, opID string, nodeID string) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) GetNode(ctx context.Context, nodeID string) (*provider.ProviderNode, error) {
	return nil, nil
}

func (p *Provider) ListNodes(ctx context.Context) ([]provider.ProviderNode, error) {
	return []provider.ProviderNode{}, nil
}

func (p *Provider) AttachDisk(ctx context.Context, opID string, nodeID string, req provider.AttachDiskRequest) (*provider.Disk, error) {
	return nil, provider.ErrProviderNotConfigured
}

func (p *Provider) DetachDisk(ctx context.Context, opID string, nodeID string, diskID string) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) ResizeDisk(ctx context.Context, opID string, diskID string, newSizeGiB int) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) CreatePublicIP(ctx context.Context, opID string, req provider.CreatePublicIPRequest) (*provider.PublicIP, error) {
	return nil, provider.ErrProviderNotConfigured
}

func (p *Provider) DeletePublicIP(ctx context.Context, opID string, ipID string) error {
	return provider.ErrProviderNotConfigured
}

func (p *Provider) GetPricing(ctx context.Context, region string, machineType string) (*provider.Pricing, error) {
	return nil, nil
}
