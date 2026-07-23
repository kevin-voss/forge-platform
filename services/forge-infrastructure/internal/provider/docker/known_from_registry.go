package docker

import (
	"context"
	"fmt"
)

// NodeResource is the minimal Node shape orphan reconciliation needs.
type NodeResource struct {
	ProviderNodeID string
}

// NodeResourceLister lists Node resources from the declarative registry.
type NodeResourceLister interface {
	ListNodes(ctx context.Context) ([]NodeResource, error)
}

// RegistryKnown adapts a NodeResourceLister to KnownNodeIDs.
type RegistryKnown struct {
	Lister NodeResourceLister
}

// ProviderNodeIDs returns the set of providerNodeId values from Node resources.
func (r *RegistryKnown) ProviderNodeIDs(ctx context.Context) (map[string]struct{}, error) {
	if r == nil || r.Lister == nil {
		return nil, fmt.Errorf("nil node lister")
	}
	items, err := r.Lister.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(items))
	for _, n := range items {
		if n.ProviderNodeID != "" {
			out[n.ProviderNodeID] = struct{}{}
		}
	}
	return out, nil
}
