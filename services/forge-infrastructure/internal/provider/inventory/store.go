package inventory

import (
	"context"
	"errors"
	"time"
)

// ErrNoFreeHost means every inventory host is already claimed (or unreachable).
var ErrNoFreeHost = errors.New("no free inventory host")

// Claim is a row in ssh_inventory_claims.
type Claim struct {
	ProviderName  string
	Address       string
	ClaimedByPool string
	ClaimedAt     *time.Time
}

// Store tracks claimed/unclaimed hosts per InfrastructureProvider.
type Store interface {
	// EnsureHosts inserts inventory addresses so they can be claimed.
	EnsureHosts(ctx context.Context, providerName string, addresses []string) error
	// ClaimNext atomically claims one free host among candidates. candidates
	// empty means any free host for the provider. Returns ErrNoFreeHost if none.
	ClaimNext(ctx context.Context, providerName, poolName string, candidates []string) (string, error)
	// Release clears the claim for address (host stays in inventory).
	Release(ctx context.Context, providerName, address string) error
	// Get returns the claim row for address, or nil if unknown.
	Get(ctx context.Context, providerName, address string) (*Claim, error)
	// List returns all claims for a provider.
	List(ctx context.Context, providerName string) ([]Claim, error)
	// CountClaimed returns how many hosts are currently claimed.
	CountClaimed(ctx context.Context, providerName string) (int, error)
}
