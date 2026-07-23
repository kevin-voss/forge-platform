package provider

import (
	"context"
	"errors"
)

// ErrProviderNotConfigured is returned when no real adapter is registered for a type.
var ErrProviderNotConfigured = errors.New("provider not configured")

// ErrNotSupported is returned when a provider does not implement an optional capability.
var ErrNotSupported = errors.New("not supported")

// ErrInventoryExhausted is returned when an adopt/release provider has no free hosts.
// Controllers must surface this as a NodePool status condition, not crash-loop.
var ErrInventoryExhausted = errors.New("inventory exhausted")

// InventoryCapacitor is optionally implemented by finite-inventory providers (ssh, bare-metal).
type InventoryCapacitor interface {
	MaxReplicas() int
}

// Provider is the seam every cloud/local adapter (23.02–23.06) implements.
// Exactly 16 methods.
type Provider interface {
	ValidateCredentials(ctx context.Context) error
	ListRegions(ctx context.Context) ([]Region, error)
	ListMachineTypes(ctx context.Context, region string) ([]MachineType, error)

	CreateNetwork(ctx context.Context, opID string, req CreateNetworkRequest) (*Network, error)
	DeleteNetwork(ctx context.Context, opID string, networkID string) error

	CreateNode(ctx context.Context, opID string, req CreateNodeRequest) (*ProviderNode, error)
	DeleteNode(ctx context.Context, opID string, nodeID string) error
	RebootNode(ctx context.Context, opID string, nodeID string) error
	GetNode(ctx context.Context, nodeID string) (*ProviderNode, error)
	ListNodes(ctx context.Context) ([]ProviderNode, error)

	AttachDisk(ctx context.Context, opID string, nodeID string, req AttachDiskRequest) (*Disk, error)
	DetachDisk(ctx context.Context, opID string, nodeID string, diskID string) error
	ResizeDisk(ctx context.Context, opID string, diskID string, newSizeGiB int) error

	CreatePublicIP(ctx context.Context, opID string, req CreatePublicIPRequest) (*PublicIP, error)
	DeletePublicIP(ctx context.Context, opID string, ipID string) error

	GetPricing(ctx context.Context, region string, machineType string) (*Pricing, error)
}

// ProviderFactory constructs a Provider from InfrastructureProvider.spec config.
type ProviderFactory func(cfg map[string]any) (Provider, error)
