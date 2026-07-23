package config

import (
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("FORGE_LOG_LEVEL", "")
	t.Setenv("FORGE_SERVICE_NAME", "")
	t.Setenv("FORGE_DATABASE_URL", "")
	t.Setenv("FORGE_DB_HOST", "")
	t.Setenv("FORGE_NETWORK_CLUSTER_CIDR", "")
	t.Setenv("FORGE_NETWORK_NODE_PREFIX_LEN", "")
	t.Setenv("FORGE_NETWORK_PROVIDER_CIDRS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port=%d", cfg.Port)
	}
	if cfg.ServiceName != "forge-network" {
		t.Fatalf("name=%q", cfg.ServiceName)
	}
	if cfg.ClusterCIDR != "10.100.0.0/16" {
		t.Fatalf("cidr=%q", cfg.ClusterCIDR)
	}
	if cfg.NodePrefixLength != 24 {
		t.Fatalf("prefix=%d", cfg.NodePrefixLength)
	}
	if cfg.DatabaseSchema != "network" {
		t.Fatalf("schema=%q", cfg.DatabaseSchema)
	}
}

func TestLoadRejectsBadCIDR(t *testing.T) {
	t.Setenv("FORGE_NETWORK_CLUSTER_CIDR", "not-a-cidr")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadProviderCIDRs(t *testing.T) {
	t.Setenv("FORGE_NETWORK_CLUSTER_CIDR", "10.100.0.0/16")
	t.Setenv("FORGE_NETWORK_PROVIDER_CIDRS", "10.0.0.0/8, 192.168.0.0/16")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.ProviderCIDRs) != 2 {
		t.Fatalf("providers=%v", cfg.ProviderCIDRs)
	}
}
