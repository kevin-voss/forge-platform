package policy

import "testing"

func strPtr(s string) *string { return &s }

func TestCompilerIngressAllowFromNamedService(t *testing.T) {
	c := &PolicyCompiler{ClusterDefault: DefaultAllowWithin}
	frontend := WorkloadPlacement{
		WorkloadID: "wl_fe", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-a", Service: strPtr("invoice-frontend"), Application: strPtr("invoice-frontend"),
		Address: "10.100.1.5",
	}
	api := WorkloadPlacement{
		WorkloadID: "wl_api", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-b", Application: strPtr("invoice-api"),
		Address: "10.100.2.5",
	}
	other := WorkloadPlacement{
		WorkloadID: "wl_other", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-c", Service: strPtr("unlisted"), Application: strPtr("unlisted"),
		Address: "10.100.3.5",
	}
	port := 8080
	in := CompileInput{
		Policies: []PolicyRow{{
			Organization: "org", Project: "proj", Environment: "production",
			TargetApplication: "invoice-api", Phase: "Ready",
			Spec: PolicySpec{
				Target: PolicyTarget{Application: "invoice-api"},
				Ingress: []IngressRule{{
					From:  PeerRef{Service: "invoice-frontend"},
					Ports: []Port{{Port: port, Protocol: "tcp"}},
				}},
			},
		}},
		Defaults:   map[envKey]string{{"org", "proj", "production"}: DefaultAllowWithin},
		Placements: []WorkloadPlacement{frontend, api, other},
	}

	rs := c.CompileForNode("node-b", 1, in)
	var allows, denies int
	for _, r := range rs.Rules {
		if r.WorkloadID != "wl_api" || r.Direction != "ingress" {
			continue
		}
		switch r.Action {
		case "allow":
			allows++
			if r.FromCIDR != "10.100.1.5/32" {
				t.Fatalf("allow from_cidr=%s want 10.100.1.5/32", r.FromCIDR)
			}
			if r.Port == nil || *r.Port != 8080 {
				t.Fatalf("port=%v", r.Port)
			}
		case "deny":
			denies++
		}
	}
	if allows != 1 {
		t.Fatalf("allows=%d want 1 (named service addresses only)", allows)
	}
	if denies < 1 {
		t.Fatal("expected catch-all deny for unmatched ingress under explicit policy")
	}
}

func TestCompilerCrossEnvironmentAlwaysDenied(t *testing.T) {
	c := &PolicyCompiler{ClusterDefault: DefaultAllowWithin}
	api := WorkloadPlacement{
		WorkloadID: "wl_api", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-b", Application: strPtr("invoice-api"), Address: "10.100.2.5",
	}
	stagingFE := WorkloadPlacement{
		WorkloadID: "wl_fe_stg", Organization: "org", Project: "proj", Environment: "staging",
		NodeID: "node-a", Service: strPtr("invoice-frontend"), Application: strPtr("invoice-frontend"),
		Address: "10.100.1.5",
	}
	// Incorrectly-scoped policy that would allow staging frontend if cross-env were not hard-denied.
	in := CompileInput{
		Policies: []PolicyRow{{
			Organization: "org", Project: "proj", Environment: "production",
			TargetApplication: "invoice-api", Phase: "Ready",
			Spec: PolicySpec{
				Target: PolicyTarget{Application: "invoice-api"},
				Ingress: []IngressRule{{
					From:  PeerRef{Service: "invoice-frontend"},
					Ports: []Port{{Port: 8080, Protocol: "tcp"}},
				}},
			},
		}},
		Defaults:   map[envKey]string{{"org", "proj", "production"}: DefaultAllowWithin},
		Placements: []WorkloadPlacement{api, stagingFE},
	}

	rs := c.CompileForNode("node-b", 2, in)
	var crossDeny bool
	var crossAllow bool
	for _, r := range rs.Rules {
		if r.WorkloadID != "wl_api" {
			continue
		}
		if r.FromCIDR == "10.100.1.5/32" || r.ToCIDR == "10.100.1.5/32" {
			if r.Action == "deny" && r.Reason == "cross-environment" {
				crossDeny = true
			}
			if r.Action == "allow" {
				crossAllow = true
			}
		}
	}
	if !crossDeny {
		t.Fatal("expected cross-environment deny for staging peer")
	}
	if crossAllow {
		t.Fatal("cross-environment traffic must not be allowed by explicit policy")
	}
}

func TestCompilerDenyAllDefaultNoPolicy(t *testing.T) {
	c := &PolicyCompiler{ClusterDefault: DefaultAllowWithin}
	api := WorkloadPlacement{
		WorkloadID: "wl_api", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-b", Application: strPtr("invoice-api"), Address: "10.100.2.5",
	}
	peer := WorkloadPlacement{
		WorkloadID: "wl_peer", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-a", Application: strPtr("other"), Address: "10.100.1.5",
	}
	in := CompileInput{
		Policies:   nil,
		Defaults:   map[envKey]string{{"org", "proj", "production"}: DefaultDenyAll},
		Placements: []WorkloadPlacement{api, peer},
	}

	rs := c.CompileForNode("node-b", 3, in)
	var denyAll bool
	for _, r := range rs.Rules {
		if r.WorkloadID == "wl_api" && r.Action == "deny" && r.Reason == "default-deny-environment" {
			denyAll = true
		}
	}
	if !denyAll {
		t.Fatal("deny-all environment default with no explicit policy must deny same-env traffic")
	}
}

func TestCompilerAllowWithinNoPolicyEmitsNoDeny(t *testing.T) {
	c := &PolicyCompiler{ClusterDefault: DefaultAllowWithin}
	api := WorkloadPlacement{
		WorkloadID: "wl_api", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-b", Application: strPtr("invoice-api"), Address: "10.100.2.5",
	}
	peer := WorkloadPlacement{
		WorkloadID: "wl_peer", Organization: "org", Project: "proj", Environment: "production",
		NodeID: "node-a", Application: strPtr("other"), Address: "10.100.1.5",
	}
	in := CompileInput{
		Policies:   nil,
		Defaults:   map[envKey]string{{"org", "proj", "production"}: DefaultAllowWithin},
		Placements: []WorkloadPlacement{api, peer},
	}
	rs := c.CompileForNode("node-b", 4, in)
	for _, r := range rs.Rules {
		if r.WorkloadID == "wl_api" && r.Action == "deny" && r.Reason != "cross-environment" {
			t.Fatalf("unexpected deny under allow-within-environment: %+v", r)
		}
	}
}
