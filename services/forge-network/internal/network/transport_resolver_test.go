package network

import "testing"

func TestResolveTransport_DockerColocated(t *testing.T) {
	got := ResolveTransport(
		NodeTransportAttrs{Membership: "hetzner-private-fsn1", DockerColocated: true},
		NodeTransportAttrs{Membership: "hetzner-private-fsn1", DockerColocated: true},
		TransportWireguard,
	)
	if got != TransportDocker {
		t.Fatalf("got %q want docker (colocation precedes membership)", got)
	}
}

func TestResolveTransport_ProviderPrivate(t *testing.T) {
	got := ResolveTransport(
		NodeTransportAttrs{Membership: "hetzner-private-fsn1"},
		NodeTransportAttrs{Membership: "hetzner-private-fsn1"},
		TransportWireguard,
	)
	if got != TransportProviderPrivate {
		t.Fatalf("got %q want provider-private", got)
	}
}

func TestResolveTransport_WireguardDifferentMembership(t *testing.T) {
	got := ResolveTransport(
		NodeTransportAttrs{Membership: "hetzner-private-fsn1"},
		NodeTransportAttrs{Membership: "aws-vpc-use1"},
		TransportWireguard,
	)
	if got != TransportWireguard {
		t.Fatalf("got %q want wireguard", got)
	}
}

func TestResolveTransport_WireguardAbsentMembership(t *testing.T) {
	cases := []struct {
		name string
		a, b NodeTransportAttrs
	}{
		{"both empty", NodeTransportAttrs{}, NodeTransportAttrs{}},
		{"one empty", NodeTransportAttrs{Membership: "hetzner-private-fsn1"}, NodeTransportAttrs{}},
		{"not colocated alone", NodeTransportAttrs{DockerColocated: true}, NodeTransportAttrs{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTransport(tc.a, tc.b, TransportWireguard)
			if got != TransportWireguard {
				t.Fatalf("got %q want wireguard", got)
			}
		})
	}
}

func TestResolveTransport_DefaultModeFallback(t *testing.T) {
	got := ResolveTransport(NodeTransportAttrs{}, NodeTransportAttrs{}, "")
	if got != TransportWireguard {
		t.Fatalf("empty default → wireguard, got %q", got)
	}
}
