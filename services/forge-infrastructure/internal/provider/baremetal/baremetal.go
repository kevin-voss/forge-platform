package baremetal

import (
	"forge.local/services/forge-infrastructure/internal/provider"
	"forge.local/services/forge-infrastructure/internal/provider/ssh"
)

// Factory returns a ProviderFactory for type "bare-metal".
// Semantics match the SSH adopt/release provider: inventory is the entire
// capacity ceiling (no elastic fallback).
func Factory(defaults ssh.Config) provider.ProviderFactory {
	return ssh.BareMetalFactory(defaults)
}

// New constructs a bare-metal Provider (adopt/release over static inventory).
func New(cfg ssh.Config) (*ssh.Provider, error) {
	cfg.TypeName = provider.TypeBareMetal
	cfg.IDPrefix = "bare-metal"
	return ssh.New(cfg)
}
