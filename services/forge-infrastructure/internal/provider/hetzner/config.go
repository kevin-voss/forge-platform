package hetzner

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	defaultOrphanGraceMinutes = 15
	defaultNetworkCIDR        = "10.1.0.0/16"
	defaultImage              = "ubuntu-24.04"
)

// SpecConfig is InfrastructureProvider.spec.config for type=hetzner.
type SpecConfig struct {
	NetworkCIDR        string
	OrphanGraceMinutes int
	Image              string
	SharedNetwork      bool // when true, one network per InfrastructureProvider; else per NodePool
}

// ValidateConfig validates InfrastructureProvider.spec.config for hetzner.
func ValidateConfig(cfg map[string]any) error {
	_, err := ParseConfig(cfg)
	return err
}

// ParseConfig extracts and validates hetzner config fields.
func ParseConfig(cfg map[string]any) (SpecConfig, error) {
	out := SpecConfig{
		NetworkCIDR:        defaultNetworkCIDR,
		OrphanGraceMinutes: defaultOrphanGraceMinutes,
		Image:              defaultImage,
	}
	if cfg == nil {
		return out, nil
	}
	if v, ok := cfg["networkCIDR"].(string); ok && strings.TrimSpace(v) != "" {
		cidr := strings.TrimSpace(v)
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return SpecConfig{}, fmt.Errorf("config.networkCIDR must be a valid CIDR: %w", err)
		}
		out.NetworkCIDR = cidr
	}
	if raw, ok := cfg["orphanGraceMinutes"]; ok && raw != nil {
		n, err := asPositiveInt(raw)
		if err != nil {
			return SpecConfig{}, fmt.Errorf("config.orphanGraceMinutes must be a positive integer: %w", err)
		}
		out.OrphanGraceMinutes = n
	}
	if v, ok := cfg["image"].(string); ok && strings.TrimSpace(v) != "" {
		out.Image = strings.TrimSpace(v)
	}
	if v, ok := cfg["sharedNetwork"].(bool); ok {
		out.SharedNetwork = v
	}
	return out, nil
}

func asPositiveInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		if n < 1 {
			return 0, fmt.Errorf("got %d", n)
		}
		return n, nil
	case int64:
		if n < 1 {
			return 0, fmt.Errorf("got %d", n)
		}
		return int(n), nil
	case float64:
		if n < 1 || n != float64(int(n)) {
			return 0, fmt.Errorf("got %v", n)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil || i < 1 {
			return 0, fmt.Errorf("got %q", n)
		}
		return int(i), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil || i < 1 {
			return 0, fmt.Errorf("got %q", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}
