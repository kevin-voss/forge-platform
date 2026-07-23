package aws

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	defaultOrphanGraceMinutes = 15
	defaultVPCCIDR            = "10.30.0.0/16"
	defaultAMI                = "ami-ubuntu-24.04"
)

// SpecConfig is InfrastructureProvider.spec.config for type=aws.
type SpecConfig struct {
	VPCCIDR            string
	OrphanGraceMinutes int
	AMI                string
	SubnetCIDR         string
}

// ValidateConfig validates InfrastructureProvider.spec.config for aws.
func ValidateConfig(cfg map[string]any) error {
	_, err := ParseConfig(cfg)
	return err
}

// ParseConfig extracts and validates aws config fields.
func ParseConfig(cfg map[string]any) (SpecConfig, error) {
	out := SpecConfig{
		VPCCIDR:            defaultVPCCIDR,
		OrphanGraceMinutes: defaultOrphanGraceMinutes,
		AMI:                defaultAMI,
	}
	if cfg == nil {
		return out, nil
	}
	cidrKey := "vpcCidr"
	if _, ok := cfg["vpcCIDR"]; ok {
		cidrKey = "vpcCIDR"
	}
	if v, ok := cfg[cidrKey].(string); ok && strings.TrimSpace(v) != "" {
		cidr := strings.TrimSpace(v)
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return SpecConfig{}, fmt.Errorf("config.vpcCidr must be a valid CIDR: %w", err)
		}
		out.VPCCIDR = cidr
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
	if v, ok := cfg["ami"].(string); ok && strings.TrimSpace(v) != "" {
		out.AMI = strings.TrimSpace(v)
	}
	if out.SubnetCIDR == "" {
		out.SubnetCIDR = deriveSubnet(out.VPCCIDR)
	}
	return out, nil
}

func deriveSubnet(vpcCIDR string) string {
	_, ipNet, err := net.ParseCIDR(vpcCIDR)
	if err != nil {
		return "10.30.1.0/24"
	}
	ip := ipNet.IP.To4()
	if ip == nil {
		return "10.30.1.0/24"
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
