package inventory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-process Store for unit tests and non-DB deployments.
type MemoryStore struct {
	mu    sync.Mutex
	hosts map[string]*Claim // key: provider\0address
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{hosts: map[string]*Claim{}}
}

func memKey(provider, address string) string {
	return provider + "\x00" + strings.ToLower(address)
}

func (s *MemoryStore) EnsureHosts(ctx context.Context, providerName string, addresses []string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, addr := range addresses {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		k := memKey(providerName, addr)
		if _, ok := s.hosts[k]; !ok {
			s.hosts[k] = &Claim{ProviderName: providerName, Address: addr}
		}
	}
	return nil
}

func (s *MemoryStore) ClaimNext(ctx context.Context, providerName, poolName string, candidates []string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	candSet := map[string]bool{}
	for _, c := range candidates {
		candSet[strings.ToLower(strings.TrimSpace(c))] = true
	}
	type entry struct {
		key  string
		addr string
	}
	var free []entry
	for k, c := range s.hosts {
		if c.ProviderName != providerName {
			continue
		}
		if c.ClaimedByPool != "" {
			continue
		}
		if len(candSet) > 0 && !candSet[strings.ToLower(c.Address)] {
			continue
		}
		free = append(free, entry{key: k, addr: c.Address})
	}
	if len(free) == 0 {
		return "", ErrNoFreeHost
	}
	sort.Slice(free, func(i, j int) bool { return free[i].addr < free[j].addr })
	pick := free[0]
	now := time.Now().UTC()
	s.hosts[pick.key].ClaimedByPool = poolName
	s.hosts[pick.key].ClaimedAt = &now
	return pick.addr, nil
}

func (s *MemoryStore) Release(ctx context.Context, providerName, address string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memKey(providerName, address)
	c, ok := s.hosts[k]
	if !ok {
		return nil
	}
	c.ClaimedByPool = ""
	c.ClaimedAt = nil
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, providerName, address string) (*Claim, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.hosts[memKey(providerName, address)]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (s *MemoryStore) List(ctx context.Context, providerName string) ([]Claim, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Claim, 0)
	for _, c := range s.hosts {
		if c.ProviderName == providerName {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out, nil
}

func (s *MemoryStore) CountClaimed(ctx context.Context, providerName string) (int, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.hosts {
		if c.ProviderName == providerName && c.ClaimedByPool != "" {
			n++
		}
	}
	return n, nil
}
