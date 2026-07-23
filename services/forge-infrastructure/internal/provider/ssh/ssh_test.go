package ssh_test

import (
	"context"
	"errors"
	"testing"

	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/baremetal"
	"forge.local/services/forge-infrastructure/internal/provider/inventory"
	"forge.local/services/forge-infrastructure/internal/provider/ssh"
	"forge.local/services/forge-infrastructure/internal/provider/sshprobe"
)

func testProvider(t *testing.T, typeName string, hosts []inventory.Host) *ssh.Provider {
	t.Helper()
	store := inventory.NewMemoryStore()
	secrets := &ssh.MapSecrets{Keys: map[string][]byte{"rack1-ssh-key": []byte("dummy-key")}}
	probe := sshprobe.NewFake()
	cfg := ssh.Config{
		ProviderName: "rack1",
		TypeName:     typeName,
		IDPrefix:     typeName,
		Inventory:    hosts,
		Store:        store,
		Prober:       probe,
		Secrets:      secrets,
		RuntimeImage: "forge/forge-runtime:local",
		ControlURL:   "http://forge-control:8080",
	}
	if typeName == provider.TypeBareMetal {
		cfg.IDPrefix = "bare-metal"
		p, err := baremetal.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg.IDPrefix = "ssh"
	p, err := ssh.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hosts() []inventory.Host {
	return []inventory.Host{
		{Address: "10.0.4.11", SSHUser: "forge", KeySecretName: "rack1-ssh-key"},
		{Address: "10.0.4.12", SSHUser: "forge", KeySecretName: "rack1-ssh-key"},
	}
}

func TestErrNotSupportedOnBothAdapters(t *testing.T) {
	for _, typeName := range []string{provider.TypeSSH, provider.TypeBareMetal} {
		p := testProvider(t, typeName, hosts())
		ctx := context.Background()
		if _, err := p.CreateNetwork(ctx, "op1", provider.CreateNetworkRequest{}); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s CreateNetwork: %v", typeName, err)
		}
		if err := p.DeleteNetwork(ctx, "op1", "n"); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s DeleteNetwork: %v", typeName, err)
		}
		if _, err := p.AttachDisk(ctx, "op1", "n", provider.AttachDiskRequest{}); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s AttachDisk: %v", typeName, err)
		}
		if err := p.DetachDisk(ctx, "op1", "n", "d"); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s DetachDisk: %v", typeName, err)
		}
		if err := p.ResizeDisk(ctx, "op1", "d", 10); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s ResizeDisk: %v", typeName, err)
		}
		if _, err := p.CreatePublicIP(ctx, "op1", provider.CreatePublicIPRequest{}); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s CreatePublicIP: %v", typeName, err)
		}
		if err := p.DeletePublicIP(ctx, "op1", "ip"); !errors.Is(err, provider.ErrNotSupported) {
			t.Fatalf("%s DeletePublicIP: %v", typeName, err)
		}
	}
}

func TestCreateDeleteAdoptRelease(t *testing.T) {
	p := testProvider(t, provider.TypeSSH, hosts())
	ctx := context.Background()

	n1, err := p.CreateNode(ctx, "op_a", provider.CreateNodeRequest{Name: "pool-0", NodePool: "pool-a"})
	if err != nil {
		t.Fatal(err)
	}
	if n1.ID != "ssh:10.0.4.11" || n1.Address != "10.0.4.11" {
		t.Fatalf("node: %+v", n1)
	}
	n2, err := p.CreateNode(ctx, "op_b", provider.CreateNodeRequest{Name: "pool-1", NodePool: "pool-a"})
	if err != nil {
		t.Fatal(err)
	}
	if n2.Address != "10.0.4.12" {
		t.Fatalf("second host: %+v", n2)
	}
	_, err = p.CreateNode(ctx, "op_c", provider.CreateNodeRequest{Name: "pool-2", NodePool: "pool-a"})
	if !errors.Is(err, provider.ErrInventoryExhausted) {
		t.Fatalf("expected ErrInventoryExhausted, got %v", err)
	}

	if err := p.DeleteNode(ctx, "op_del", n1.ID); err != nil {
		t.Fatal(err)
	}
	// Released host claimable by a different pool.
	n3, err := p.CreateNode(ctx, "op_d", provider.CreateNodeRequest{Name: "pool-b-0", NodePool: "pool-b"})
	if err != nil {
		t.Fatal(err)
	}
	if n3.Address != "10.0.4.11" {
		t.Fatalf("expected reclaimed 10.0.4.11, got %+v", n3)
	}
}

func TestBareMetalMaxReplicas(t *testing.T) {
	p := testProvider(t, provider.TypeBareMetal, hosts()[:1])
	if p.MaxReplicas() != 1 {
		t.Fatalf("max=%d", p.MaxReplicas())
	}
}

func TestRegistryRegistersSSHAndBareMetal(t *testing.T) {
	reg := provider.NewRegistry(nil)
	defaults := ssh.Config{
		Store:   inventory.NewMemoryStore(),
		Prober:  sshprobe.NewFake(),
		Secrets: &ssh.MapSecrets{Keys: map[string][]byte{"k": []byte("x")}},
	}
	reg.Register(provider.TypeSSH, ssh.Factory(defaults))
	reg.Register(provider.TypeBareMetal, baremetal.Factory(defaults))
	if !reg.Has(provider.TypeSSH) || !reg.Has(provider.TypeBareMetal) {
		t.Fatal("expected ssh and bare-metal registered")
	}
	cfg := map[string]any{
		"providerName": "r1",
		"inventory": []any{
			map[string]any{
				"address":         "10.0.4.11",
				"sshUser":         "forge",
				"sshKeySecretRef": map[string]any{"name": "k"},
			},
		},
	}
	p, err := reg.Resolve(provider.TypeBareMetal, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(provider.InventoryCapacitor); !ok {
		t.Fatal("expected InventoryCapacitor")
	}
}
