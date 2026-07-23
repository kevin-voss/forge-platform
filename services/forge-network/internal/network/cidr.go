package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

// Plan describes how a cluster CIDR is carved into node blocks and workload addresses.
type Plan struct {
	ClusterCIDR      netip.Prefix
	NodePrefixLength int
}

// ParsePlan validates cluster CIDR + node prefix length.
func ParsePlan(clusterCIDR string, nodePrefixLength int) (Plan, error) {
	pfx, err := netip.ParsePrefix(clusterCIDR)
	if err != nil {
		return Plan{}, fmt.Errorf("cluster cidr: %w", err)
	}
	pfx = pfx.Masked()
	if !pfx.Addr().Is4() {
		return Plan{}, fmt.Errorf("cluster cidr must be IPv4")
	}
	if nodePrefixLength < pfx.Bits() || nodePrefixLength > 28 {
		return Plan{}, fmt.Errorf("node prefix length %d invalid for cluster /%d", nodePrefixLength, pfx.Bits())
	}
	return Plan{ClusterCIDR: pfx, NodePrefixLength: nodePrefixLength}, nil
}

// NodeBlockCount returns how many node blocks fit in the cluster CIDR.
func (p Plan) NodeBlockCount() int {
	bits := p.NodePrefixLength - p.ClusterCIDR.Bits()
	if bits < 0 || bits > 31 {
		return 0
	}
	return 1 << bits
}

// NodeBlock returns the node /N at index. Index 0 is reserved (skipped by the allocator);
// valid lease indexes are 1 .. NodeBlockCount()-1.
func (p Plan) NodeBlock(index int) (netip.Prefix, error) {
	if index < 0 || index >= p.NodeBlockCount() {
		return netip.Prefix{}, fmt.Errorf("node block index %d out of range [0,%d)", index, p.NodeBlockCount())
	}
	base := p.ClusterCIDR.Addr().As4()
	hostBits := 32 - p.NodePrefixLength
	blockSize := uint32(1) << hostBits
	addr := binary.BigEndian.Uint32(base[:]) + uint32(index)*blockSize
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], addr)
	ip := netip.AddrFrom4(b)
	return netip.PrefixFrom(ip, p.NodePrefixLength), nil
}

// GatewayForBlock returns the conventional .1 gateway inside a node block.
func GatewayForBlock(block netip.Prefix) (netip.Addr, error) {
	if !block.IsValid() || !block.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("invalid block")
	}
	base := block.Addr().As4()
	n := binary.BigEndian.Uint32(base[:])
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n+1)
	return netip.AddrFrom4(b), nil
}

// WorkloadAddress returns the host address at offset within a node block.
// Offset 0 = network, 1 = gateway; usable workload offsets are 2 .. (2^(32-prefix)-2).
func WorkloadAddress(block netip.Prefix, offset int) (netip.Addr, error) {
	if !block.IsValid() || !block.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("invalid block")
	}
	hostBits := 32 - block.Bits()
	maxHosts := 1 << hostBits
	if offset < 0 || offset >= maxHosts {
		return netip.Addr{}, fmt.Errorf("workload offset %d out of range", offset)
	}
	base := block.Addr().As4()
	n := binary.BigEndian.Uint32(base[:])
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n+uint32(offset))
	return netip.AddrFrom4(b), nil
}

// MaxWorkloadOffset is the last usable host offset (broadcast-1).
func MaxWorkloadOffset(block netip.Prefix) int {
	hostBits := 32 - block.Bits()
	return (1 << hostBits) - 2
}

// FirstWorkloadOffset is the first usable host (.2 after network+gateway).
const FirstWorkloadOffset = 2

// Overlaps reports whether two CIDR strings overlap.
func Overlaps(a, b string) (bool, error) {
	_, na, err := net.ParseCIDR(a)
	if err != nil {
		return false, err
	}
	_, nb, err := net.ParseCIDR(b)
	if err != nil {
		return false, err
	}
	return na.Contains(nb.IP) || nb.Contains(na.IP), nil
}

// ContainsAddr reports whether addr is inside cidr.
func ContainsAddr(cidr, addr string) (bool, error) {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, err
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false, fmt.Errorf("invalid address %q", addr)
	}
	return n.Contains(ip), nil
}
