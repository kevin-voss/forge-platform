package inventory

import (
	"fmt"
	"strings"
)

// Host is one operator-declared inventory entry.
type Host struct {
	Address       string
	SSHUser       string
	KeySecretName string
	KeySecretRef  map[string]any // raw sshKeySecretRef object (name required)
}

// ParseConfig extracts inventory hosts from InfrastructureProvider.spec.config.
func ParseConfig(cfg map[string]any) ([]Host, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	raw, ok := cfg["inventory"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("config.inventory is required")
	}
	list, ok := raw.([]any)
	if !ok {
		// tolerate []map from typed tests
		if typed, ok2 := raw.([]map[string]any); ok2 {
			list = make([]any, len(typed))
			for i := range typed {
				list[i] = typed[i]
			}
		} else {
			return nil, fmt.Errorf("config.inventory must be an array")
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("config.inventory must not be empty")
	}
	out := make([]Host, 0, len(list))
	seen := map[string]bool{}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config.inventory[%d] must be an object", i)
		}
		h, err := parseHost(m, i)
		if err != nil {
			return nil, err
		}
		addr := strings.ToLower(h.Address)
		if seen[addr] {
			return nil, fmt.Errorf("config.inventory[%d]: duplicate address %q within provider", i, h.Address)
		}
		seen[addr] = true
		out = append(out, h)
	}
	return out, nil
}

func parseHost(m map[string]any, idx int) (Host, error) {
	addr := strings.TrimSpace(stringField(m, "address"))
	if addr == "" {
		return Host{}, fmt.Errorf("config.inventory[%d].address is required", idx)
	}
	user := strings.TrimSpace(stringField(m, "sshUser"))
	if user == "" {
		return Host{}, fmt.Errorf("config.inventory[%d].sshUser is required", idx)
	}
	// Reject inline keys — credentials must be secret refs only.
	for _, forbidden := range []string{"sshKey", "privateKey", "private_key", "key", "password"} {
		if _, ok := m[forbidden]; ok {
			return Host{}, fmt.Errorf("config.inventory[%d]: inline %q is forbidden; use sshKeySecretRef", idx, forbidden)
		}
	}
	refRaw, ok := m["sshKeySecretRef"]
	if !ok || refRaw == nil {
		return Host{}, fmt.Errorf("config.inventory[%d].sshKeySecretRef is required", idx)
	}
	ref, ok := refRaw.(map[string]any)
	if !ok {
		return Host{}, fmt.Errorf("config.inventory[%d].sshKeySecretRef must be an object", idx)
	}
	name := strings.TrimSpace(stringField(ref, "name"))
	if name == "" {
		return Host{}, fmt.Errorf("config.inventory[%d].sshKeySecretRef.name is required", idx)
	}
	return Host{
		Address:       addr,
		SSHUser:       user,
		KeySecretName: name,
		KeySecretRef:  ref,
	}, nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
