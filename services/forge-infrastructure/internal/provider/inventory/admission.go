package inventory

import (
	"fmt"
	"strings"
)

// ProviderInventory is one InfrastructureProvider's declared host set for admission.
type ProviderInventory struct {
	Name  string
	Type  string
	Hosts []Host
}

// ValidateSchema validates inventory shape for ssh / bare-metal providers.
func ValidateSchema(typeName string, cfg map[string]any) error {
	switch typeName {
	case "ssh", "bare-metal":
	default:
		return nil
	}
	_, err := ParseConfig(cfg)
	return err
}

// CheckDuplicateAddresses rejects a candidate when any host address already
// appears in another InfrastructureProvider inventory.
func CheckDuplicateAddresses(existing []ProviderInventory, candidate ProviderInventory) error {
	if err := ValidateSchema(candidate.Type, map[string]any{
		"inventory": hostsToAny(candidate.Hosts),
	}); err != nil && len(candidate.Hosts) == 0 {
		// candidate may already be parsed; fall through to address check
		_ = err
	}
	owned := map[string]string{} // address -> provider name
	for _, p := range existing {
		if p.Name == candidate.Name {
			continue
		}
		switch p.Type {
		case "ssh", "bare-metal":
		default:
			continue
		}
		for _, h := range p.Hosts {
			addr := strings.ToLower(strings.TrimSpace(h.Address))
			if addr == "" {
				continue
			}
			owned[addr] = p.Name
		}
	}
	for _, h := range candidate.Hosts {
		addr := strings.ToLower(strings.TrimSpace(h.Address))
		if other, ok := owned[addr]; ok {
			return fmt.Errorf("host address %q is already declared by InfrastructureProvider %q", h.Address, other)
		}
	}
	return nil
}

// AdmitCreate validates schema + cross-provider address uniqueness.
func AdmitCreate(existing []ProviderInventory, typeName, name string, cfg map[string]any) error {
	switch typeName {
	case "ssh", "bare-metal":
	default:
		return nil
	}
	hosts, err := ParseConfig(cfg)
	if err != nil {
		return err
	}
	return CheckDuplicateAddresses(existing, ProviderInventory{
		Name:  name,
		Type:  typeName,
		Hosts: hosts,
	})
}

func hostsToAny(hosts []Host) []any {
	out := make([]any, len(hosts))
	for i, h := range hosts {
		ref := h.KeySecretRef
		if ref == nil {
			ref = map[string]any{"name": h.KeySecretName}
		}
		out[i] = map[string]any{
			"address":         h.Address,
			"sshUser":         h.SSHUser,
			"sshKeySecretRef": ref,
		}
	}
	return out
}
