package controller_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"forge.local/services/forge-infrastructure/internal/controller"
	"forge.local/services/forge-infrastructure/internal/operations"
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/noop"
	"forge.local/services/forge-infrastructure/internal/registryclient"
)

type memLedger struct {
	mu   sync.Mutex
	rows map[string]*operations.Operation // key: provider|natural
	ids  *operations.Generator
}

func newMemLedger() *memLedger {
	return &memLedger{rows: map[string]*operations.Operation{}, ids: operations.NewGenerator()}
}

func (m *memLedger) key(p, n string) string { return p + "|" + n }

func (m *memLedger) Begin(ctx context.Context, providerName, kind, targetKind, naturalKey string, request any) (*operations.BeginResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(providerName, naturalKey)
	if existing, ok := m.rows[k]; ok {
		skip := existing.Status == operations.StatusPending || existing.Status == operations.StatusSucceeded
		return &operations.BeginResult{Op: existing, AlreadyExists: true, SkipProvider: skip}, nil
	}
	b, _ := json.Marshal(request)
	op := &operations.Operation{
		ID: m.ids.NewOpID(), ProviderName: providerName, Kind: kind, TargetKind: targetKind,
		NaturalKey: naturalKey, Request: b, Status: operations.StatusPending,
	}
	m.rows[k] = op
	return &operations.BeginResult{Op: op}, nil
}

func (m *memLedger) Complete(ctx context.Context, opID string, result any, callErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range m.rows {
		if op.ID == opID {
			if callErr != nil {
				op.Status = operations.StatusFailed
				msg := callErr.Error()
				op.Error = &msg
			} else {
				op.Status = operations.StatusSucceeded
				if result != nil {
					b, _ := json.Marshal(result)
					op.Result = b
				}
			}
			return nil
		}
	}
	return nil
}

func (m *memLedger) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

type fakeRegistry struct {
	mu        sync.Mutex
	providers map[string]registryclient.Resource
	pools     map[string]registryclient.Resource
	nodes     []registryclient.Resource
	statuses  []map[string]any
}

func (f *fakeRegistry) List(ctx context.Context, plural, labelSelector string) ([]registryclient.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch plural {
	case "nodepools":
		out := make([]registryclient.Resource, 0, len(f.pools))
		for _, p := range f.pools {
			out = append(out, p)
		}
		return out, nil
	case "nodes":
		return append([]registryclient.Resource{}, f.nodes...), nil
	case "infrastructureproviders":
		out := make([]registryclient.Resource, 0, len(f.providers))
		for _, p := range f.providers {
			out = append(out, p)
		}
		return out, nil
	}
	return nil, nil
}

func (f *fakeRegistry) Get(ctx context.Context, plural, name string) (*registryclient.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch plural {
	case "infrastructureproviders":
		if p, ok := f.providers[name]; ok {
			cp := p
			return &cp, nil
		}
	case "nodepools":
		if p, ok := f.pools[name]; ok {
			cp := p
			return &cp, nil
		}
	case "nodes":
		for _, n := range f.nodes {
			if n.Metadata.Name == name {
				cp := n
				return &cp, nil
			}
		}
	}
	return nil, nil
}

func (f *fakeRegistry) PutStatus(ctx context.Context, plural, name, resourceVersion string, status map[string]any) (*registryclient.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, status)
	if plural == "nodepools" {
		if p, ok := f.pools[name]; ok {
			p.Status = status
			f.pools[name] = p
			return &p, nil
		}
	}
	return &registryclient.Resource{Metadata: registryclient.Metadata{Name: name, ResourceVersion: resourceVersion}, Status: status}, nil
}

func (f *fakeRegistry) Create(ctx context.Context, plural string, res registryclient.Resource) (*registryclient.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if plural == "nodes" {
		f.nodes = append(f.nodes, res)
	}
	return &res, nil
}

type countingProvider struct {
	provider.Provider
	creates int
	deletes []string
}

func (p *countingProvider) CreateNode(ctx context.Context, opID string, req provider.CreateNodeRequest) (*provider.ProviderNode, error) {
	p.creates++
	return &provider.ProviderNode{ID: "prov-" + req.Name, Name: req.Name}, nil
}

func (p *countingProvider) DeleteNode(ctx context.Context, opID string, nodeID string) error {
	p.deletes = append(p.deletes, nodeID)
	return nil
}

type fixedResolver struct {
	p        provider.Provider
	typeName string
	has      bool
}

func (r *fixedResolver) Resolve(typeName string, cfg map[string]any) (provider.Provider, error) {
	return r.p, nil
}
func (r *fixedResolver) Has(typeName string) bool { return r.has }

func TestReconcileScaleUpSingleCreate(t *testing.T) {
	cp := &countingProvider{Provider: &noop.Provider{}}
	// Override mutating methods via embedding — need concrete type that implements CreateNode.
	// countingProvider embeds noop which returns ErrProviderNotConfigured — method promotion
	// means countingProvider.CreateNode is used. Good.

	reg := &fakeRegistry{
		providers: map[string]registryclient.Resource{
			"docker-local": {
				Metadata: registryclient.Metadata{Name: "docker-local"},
				Spec:     map[string]any{"type": "docker"},
			},
		},
		pools: map[string]registryclient.Resource{
			"pool-a": {
				Metadata: registryclient.Metadata{Name: "pool-a", Generation: 1, ResourceVersion: "1"},
				Spec: map[string]any{
					"providerRef": "docker-local",
					"replicas":    3,
					"machineType": "docker-small",
					"region":      "local",
				},
			},
		},
		nodes: []registryclient.Resource{
			{
				Metadata: registryclient.Metadata{Name: "pool-a-0", Labels: map[string]string{"forge.local/node-pool": "pool-a"}},
				Spec:     map[string]any{"nodePoolRef": "pool-a", "providerNodeId": "docker:0"},
				Status:   map[string]any{"phase": "Ready"},
			},
		},
	}
	ledger := newMemLedger()
	ctrl := &controller.NodePoolController{
		Registry:  reg,
		Ledger:    ledger,
		Providers: &fixedResolver{p: cp, typeName: "docker", has: true},
	}
	pool := reg.pools["pool-a"]
	if err := ctrl.Reconcile(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if cp.creates != 1 {
		t.Fatalf("expected exactly 1 CreateNode, got %d", cp.creates)
	}
	if ledger.count() != 1 {
		t.Fatalf("expected 1 ledger row, got %d", ledger.count())
	}
}

func TestReconcileScaleDownSingleDelete(t *testing.T) {
	cp := &countingProvider{Provider: &noop.Provider{}}
	reg := &fakeRegistry{
		providers: map[string]registryclient.Resource{
			"docker-local": {
				Metadata: registryclient.Metadata{Name: "docker-local"},
				Spec:     map[string]any{"type": "docker"},
			},
		},
		pools: map[string]registryclient.Resource{
			"pool-a": {
				Metadata: registryclient.Metadata{Name: "pool-a", Generation: 1, ResourceVersion: "1"},
				Spec: map[string]any{
					"providerRef": "docker-local",
					"replicas":    1,
					"machineType": "docker-small",
				},
			},
		},
		nodes: []registryclient.Resource{
			{
				Metadata: registryclient.Metadata{Name: "pool-a-0"},
				Spec:     map[string]any{"nodePoolRef": "pool-a", "providerNodeId": "docker:0"},
				Status:   map[string]any{"phase": "Ready"},
			},
			{
				Metadata: registryclient.Metadata{Name: "pool-a-1"},
				Spec:     map[string]any{"nodePoolRef": "pool-a", "providerNodeId": "docker:1"},
				Status:   map[string]any{"phase": "Ready"},
			},
			{
				Metadata: registryclient.Metadata{Name: "pool-a-2"},
				Spec:     map[string]any{"nodePoolRef": "pool-a", "providerNodeId": "docker:2"},
				Status:   map[string]any{"phase": "Ready"},
			},
		},
	}
	ledger := newMemLedger()
	ctrl := &controller.NodePoolController{
		Registry:  reg,
		Ledger:    ledger,
		Providers: &fixedResolver{p: cp, typeName: "docker", has: true},
	}
	if err := ctrl.Reconcile(context.Background(), reg.pools["pool-a"]); err != nil {
		t.Fatal(err)
	}
	if len(cp.deletes) != 1 {
		t.Fatalf("expected exactly 1 DeleteNode, got %v", cp.deletes)
	}
	// most recently created by name → pool-a-2
	if cp.deletes[0] != "docker:2" {
		t.Fatalf("expected delete docker:2, got %s", cp.deletes[0])
	}
}

func TestReconcileUnknownProviderCondition(t *testing.T) {
	reg := &fakeRegistry{
		providers: map[string]registryclient.Resource{
			"weird": {
				Metadata: registryclient.Metadata{Name: "weird"},
				Spec:     map[string]any{"type": "unknown-provider"},
			},
		},
		pools: map[string]registryclient.Resource{
			"pool-b": {
				Metadata: registryclient.Metadata{Name: "pool-b", Generation: 1, ResourceVersion: "1"},
				Spec: map[string]any{
					"providerRef": "weird",
					"replicas":    1,
				},
			},
		},
	}
	r := provider.NewRegistry(noop.Factory)
	ctrl := &controller.NodePoolController{
		Registry:  reg,
		Ledger:    newMemLedger(),
		Providers: r,
	}
	if err := ctrl.Reconcile(context.Background(), reg.pools["pool-b"]); err != nil {
		t.Fatal(err)
	}
	if len(reg.statuses) == 0 {
		t.Fatal("expected status write")
	}
	conds, _ := reg.statuses[len(reg.statuses)-1]["conditions"].([]map[string]any)
	if len(conds) == 0 {
		t.Fatalf("expected conditions: %#v", reg.statuses[len(reg.statuses)-1])
	}
	if conds[0]["reason"] != "ProviderNotConfigured" || conds[0]["status"] != "False" {
		t.Fatalf("unexpected condition: %#v", conds[0])
	}
}
