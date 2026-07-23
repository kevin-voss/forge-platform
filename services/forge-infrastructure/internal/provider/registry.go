package provider

import (
	"fmt"
	"sync"
)

// Known provider type keys (InfrastructureProvider.spec.type).
const (
	TypeDocker    = "docker"
	TypeSSH       = "ssh"
	TypeBareMetal = "bare-metal"
	TypeHetzner   = "hetzner"
	TypeAWS       = "aws"
	TypeAzure     = "azure"
)

// Registry maps InfrastructureProvider.spec.type → ProviderFactory.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
	fallback  ProviderFactory
}

// NewRegistry returns a registry with optional default/no-op factory.
func NewRegistry(fallback ProviderFactory) *Registry {
	return &Registry{
		factories: make(map[string]ProviderFactory),
		fallback:  fallback,
	}
}

// Register binds a factory to a provider type string.
func (r *Registry) Register(typeName string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[typeName] = factory
}

// Resolve constructs a Provider for the given type. Unknown types use the
// fallback factory when set; otherwise returns ErrProviderNotConfigured.
func (r *Registry) Resolve(typeName string, cfg map[string]any) (Provider, error) {
	r.mu.RLock()
	factory, ok := r.factories[typeName]
	fallback := r.fallback
	r.mu.RUnlock()

	if ok && factory != nil {
		return factory(cfg)
	}
	if fallback != nil {
		return fallback(cfg)
	}
	return nil, fmt.Errorf("%w: type %q", ErrProviderNotConfigured, typeName)
}

// Has reports whether a non-fallback factory is registered for typeName.
func (r *Registry) Has(typeName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[typeName]
	return ok
}
