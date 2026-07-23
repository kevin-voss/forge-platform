package azure

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	defaultOrphanGraceMinutes = 15
	defaultVNetCIDR           = "10.40.0.0/16"
	defaultImage              = "Canonical:0001-com-ubuntu-server-noble:24_04-lts:latest"
	defaultResourceGroup      = "forge"
)

// SpecConfig is InfrastructureProvider.spec.config for type=azure.
type SpecConfig struct {
	VNetCIDR           string
	OrphanGraceMinutes int
	Image              string
	ResourceGroup      string
	SubnetCIDR         string
}

// ValidateConfig validates InfrastructureProvider.spec.config for azure.
func ValidateConfig(cfg map[string]any) error {
	_, err := ParseConfig(cfg)
	return err
}

// ParseConfig extracts and validates azure config fields.
func ParseConfig(cfg map[string]any) (SpecConfig, error) {
	out := SpecConfig{
		VNetCIDR:           defaultVNetCIDR,
		OrphanGraceMinutes: defaultOrphanGraceMinutes,
		Image:              defaultImage,
		ResourceGroup:      defaultResourceGroup,
	}
	if cfg == nil {
		return out, nil
	}
	cidrKey := "vnetCidr"
	if _, ok := cfg["vnetCIDR"]; ok {
		cidrKey = "vnetCIDR"
	}
	if v, ok := cfg[cidrKey].(string); ok && strings.TrimSpace(v) != "" {
		cidr := strings.TrimSpace(v)
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return SpecConfig{}, fmt.Errorf("config.vnetCidr must be a valid CIDR: %w", err)
		}
		out.VNetCIDR = cidr
	}
	if v, ok := cfg["subnetCidr"].(string); ok && strings.TrimSpace(v) != "" {
		cidr := strings.TrimSpace(v)
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return SpecConfig{}, fmt.Errorf("config.subnetCidr must be a valid CIDR: %w", err)
		}
		out.SubnetCIDR = cidr
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
	if v, ok := cfg["resourceGroup"].(string); ok && strings.TrimSpace(v) != "" {
		out.ResourceGroup = strings.TrimSpace(v)
	}
	if out.SubnetCIDR == "" {
		out.SubnetCIDR = deriveSubnet(out.VNetCIDR)
	}
	return out, nil
}

func deriveSubnet(vnetCIDR string) string {
	_, ipNet, err := net.ParseCIDR(vnetCIDR)
	if err != nil {
		return "10.40.1.0/24"
	}
	ip := ipNet.IP.To4()
	if ip == nil {
		return "10.40.1.0/24"
	}
	return fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2]+1)
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
