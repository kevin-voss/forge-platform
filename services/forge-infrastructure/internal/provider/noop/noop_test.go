package noop_test

import (
	"context"
	"errors"
	"testing"

	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/noop"
)

func TestNoopMutatingReturnsNotConfigured(t *testing.T) {
	p := &noop.Provider{}
	ctx := context.Background()

	if err := p.ValidateCredentials(ctx); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("ValidateCredentials: %v", err)
	}
	if _, err := p.CreateNetwork(ctx, "op_1", provider.CreateNetworkRequest{Name: "n"}); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if err := p.DeleteNetwork(ctx, "op_1", "net"); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("DeleteNetwork: %v", err)
	}
	if _, err := p.CreateNode(ctx, "op_1", provider.CreateNodeRequest{Name: "n"}); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("CreateNode: %v", err)
	}
	if err := p.DeleteNode(ctx, "op_1", "n"); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("DeleteNode: %v", err)
	}
	if err := p.RebootNode(ctx, "op_1", "n"); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("RebootNode: %v", err)
	}
	if _, err := p.AttachDisk(ctx, "op_1", "n", provider.AttachDiskRequest{SizeGiB: 1}); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("AttachDisk: %v", err)
	}
	if err := p.DetachDisk(ctx, "op_1", "n", "d"); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("DetachDisk: %v", err)
	}
	if err := p.ResizeDisk(ctx, "op_1", "d", 2); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("ResizeDisk: %v", err)
	}
	if _, err := p.CreatePublicIP(ctx, "op_1", provider.CreatePublicIPRequest{}); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("CreatePublicIP: %v", err)
	}
	if err := p.DeletePublicIP(ctx, "op_1", "ip"); !errors.Is(err, provider.ErrProviderNotConfigured) {
		t.Fatalf("DeletePublicIP: %v", err)
	}
}

func TestNoopReadsReturnEmptyNotError(t *testing.T) {
	p := &noop.Provider{}
	ctx := context.Background()

	regions, err := p.ListRegions(ctx)
	if err != nil || len(regions) != 0 {
		t.Fatalf("ListRegions: %v %#v", err, regions)
	}
	types, err := p.ListMachineTypes(ctx, "local")
	if err != nil || len(types) != 0 {
		t.Fatalf("ListMachineTypes: %v %#v", err, types)
	}
	node, err := p.GetNode(ctx, "missing")
	if err != nil || node != nil {
		t.Fatalf("GetNode: %v %#v", err, node)
	}
	nodes, err := p.ListNodes(ctx)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("ListNodes: %v %#v", err, nodes)
	}
	pricing, err := p.GetPricing(ctx, "local", "docker-small")
	if err != nil || pricing != nil {
		t.Fatalf("GetPricing: %v %#v", err, pricing)
	}
}
