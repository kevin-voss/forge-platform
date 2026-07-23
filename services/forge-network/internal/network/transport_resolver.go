package network

import "strings"

// Transport mode for a directed node pair.
const (
	TransportDocker          = "docker"
	TransportProviderPrivate = "provider-private"
	TransportWireguard       = "wireguard"
)

// NodeTransportAttrs are the per-node inputs to TransportResolver.
type NodeTransportAttrs struct {
	Membership      string
	DockerColocated bool
}

// ResolveTransport picks the transport for an ordered pair (from → to).
//
// Precedence:
//  1. both docker_colocated → docker
//  2. same non-empty network_membership → provider-private
//  3. otherwise → defaultMode (cluster fallback; typically wireguard)
func ResolveTransport(from, to NodeTransportAttrs, defaultMode string) string {
	if from.DockerColocated && to.DockerColocated {
		return TransportDocker
	}
	a := strings.TrimSpace(from.Membership)
	b := strings.TrimSpace(to.Membership)
	if a != "" && a == b {
		return TransportProviderPrivate
	}
	mode := strings.TrimSpace(defaultMode)
	if mode == "" {
		return TransportWireguard
	}
	switch mode {
	case TransportDocker, TransportProviderPrivate, TransportWireguard:
		return mode
	default:
		return TransportWireguard
	}
}
