package inventory_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"forge.local/services/forge-infrastructure/internal/provider/inventory"
)

func TestMemoryClaimReleaseExclusive(t *testing.T) {
	store := inventory.NewMemoryStore()
	ctx := context.Background()
	if err := store.EnsureHosts(ctx, "rack1", []string{"10.0.4.11"}); err != nil {
		t.Fatal(err)
	}

	var winners atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pool := "pool-a"
			if i%2 == 0 {
				pool = "pool-b"
			}
			addr, err := store.ClaimNext(ctx, "rack1", pool, nil)
			if err == nil && addr != "" {
				winners.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("expected exactly 1 winner for last free host, got %d", winners.Load())
	}

	if err := store.Release(ctx, "rack1", "10.0.4.11"); err != nil {
		t.Fatal(err)
	}
	addr, err := store.ClaimNext(ctx, "rack1", "pool-b", nil)
	if err != nil || addr != "10.0.4.11" {
		t.Fatalf("reclaim after release: addr=%q err=%v", addr, err)
	}
}

func TestParseConfigRejectsInlineKey(t *testing.T) {
	_, err := inventory.ParseConfig(map[string]any{
		"inventory": []any{
			map[string]any{
				"address":         "10.0.4.11",
				"sshUser":         "forge",
				"sshKey":          "-----BEGIN OPENSSH PRIVATE KEY-----",
				"sshKeySecretRef": map[string]any{"name": "k"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected inline key rejection")
	}
}

func TestAdmitCreateRejectsDuplicateAddress(t *testing.T) {
	existing := []inventory.ProviderInventory{
		{
			Name: "rack1",
			Type: "bare-metal",
			Hosts: []inventory.Host{
				{Address: "10.0.4.11", SSHUser: "forge", KeySecretName: "k"},
			},
		},
	}
	err := inventory.AdmitCreate(existing, "ssh", "rack2", map[string]any{
		"inventory": []any{
			map[string]any{
				"address":         "10.0.4.11",
				"sshUser":         "forge",
				"sshKeySecretRef": map[string]any{"name": "k2"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate address rejection")
	}
}

func TestValidateSchemaInventory(t *testing.T) {
	err := inventory.ValidateSchema("bare-metal", map[string]any{
		"inventory": []any{
			map[string]any{
				"address":         "10.0.4.11",
				"sshUser":         "forge",
				"sshKeySecretRef": map[string]any{"name": "rack1-ssh-key"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := inventory.ValidateSchema("bare-metal", map[string]any{}); err == nil {
		t.Fatal("expected missing inventory error")
	}
}
