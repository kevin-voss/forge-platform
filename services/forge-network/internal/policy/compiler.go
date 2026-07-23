package policy

import (
	"fmt"
	"strings"
)

// PolicyCompiler compiles NetworkPolicy + placements + leases into per-node rules.
//
// Evaluation order (most → least specific):
//  1. Cross-environment traffic → always denied.
//  2. Explicit NetworkPolicy rule matching the destination application → deny
//     takes precedence over allow at equal specificity.
//  3. No matching explicit rule → the environment's default
//     (allow-within-environment or deny-all).
type PolicyCompiler struct {
	ClusterDefault string // allow-within-environment | deny-all
}

// CompileForNode builds the rule set for one node.
func (c *PolicyCompiler) CompileForNode(nodeID string, generation int64, in CompileInput) NodeRuleSet {
	clusterDef := c.ClusterDefault
	if clusterDef == "" {
		clusterDef = DefaultAllowWithin
	}
	if in.ClusterDef != "" {
		clusterDef = in.ClusterDef
	}

	defaults := in.Defaults
	if defaults == nil {
		defaults = map[envKey]string{}
	}

	byApp := indexPlacementsByApp(in.Placements)
	byService := indexPlacementsByService(in.Placements)
	byDatabase := indexPlacementsByDatabase(in.Placements)
	byQueue := indexPlacementsByQueue(in.Placements)

	policiesByTarget := map[envKey][]PolicyRow{}
	for _, p := range in.Policies {
		k := envKey{p.Organization, p.Project, p.Environment}
		policiesByTarget[k] = append(policiesByTarget[k], p)
	}

	var rules []CompiledRule

	// Emit rules for every workload placed on this node that is an application target.
	for _, local := range in.Placements {
		if local.NodeID != nodeID || local.Application == nil || *local.Application == "" {
			continue
		}
		app := *local.Application
		ek := envKey{local.Organization, local.Project, local.Environment}
		envDefault := defaults[ek]
		if envDefault == "" {
			envDefault = clusterDef
		}

		// Policies targeting this application in the same env.
		var matching []PolicyRow
		for _, p := range policiesByTarget[ek] {
			if p.TargetApplication == app && p.Phase != "Failed" {
				matching = append(matching, p)
			}
		}

		// Cross-environment: deny all peer workloads from other environments.
		for _, peer := range in.Placements {
			if peer.WorkloadID == local.WorkloadID || peer.Address == "" {
				continue
			}
			if sameEnvironment(local, peer) {
				continue
			}
			// Unconditional cross-env deny (cannot be overridden by policy).
			cidr := hostCIDR(peer.Address)
			rules = append(rules, CompiledRule{
				WorkloadID: local.WorkloadID,
				Direction:  "ingress",
				FromCIDR:   cidr,
				Action:     "deny",
				Reason:     "cross-environment",
			})
			rules = append(rules, CompiledRule{
				WorkloadID: local.WorkloadID,
				Direction:  "egress",
				ToCIDR:     cidr,
				Action:     "deny",
				Reason:     "cross-environment",
			})
		}

		if len(matching) == 0 {
			// No explicit policy → environment default for same-env traffic.
			if envDefault == DefaultDenyAll {
				rules = append(rules, CompiledRule{
					WorkloadID: local.WorkloadID,
					Direction:  "ingress",
					FromCIDR:   "0.0.0.0/0",
					Action:     "deny",
					Reason:     "default-deny-environment",
				})
				rules = append(rules, CompiledRule{
					WorkloadID: local.WorkloadID,
					Direction:  "egress",
					ToCIDR:     "0.0.0.0/0",
					Action:     "deny",
					Reason:     "default-deny-environment",
				})
			}
			// allow-within-environment: no deny rules for same-env (implicit allow).
			continue
		}

		// Explicit policies: emit allow for matching peers.
		// An explicit ingress/egress section narrows the environment default (allowlist).
		// Traffic with no matching explicit rule falls through to the environment default
		// only when the policy does not declare that direction.
		hasExplicitIngress := false
		hasExplicitEgress := false

		for _, pol := range matching {
			for _, ing := range pol.Spec.Ingress {
				hasExplicitIngress = true
				peers := resolveIngressPeers(ing.From, byService, local)
				for _, peer := range peers {
					if !sameEnvironment(local, peer) {
						// Cross-env never allowed even if policy incorrectly names them.
						continue
					}
					if peer.Address == "" {
						continue
					}
					cidr := hostCIDR(peer.Address)
					if len(ing.Ports) == 0 {
						rules = append(rules, CompiledRule{
							WorkloadID: local.WorkloadID,
							Direction:  "ingress",
							FromCIDR:   cidr,
							Action:     "allow",
							Reason:     "explicit-policy",
						})
						continue
					}
					for _, port := range ing.Ports {
						p := port.Port
						proto := strings.ToLower(port.Protocol)
						if proto == "" {
							proto = "tcp"
						}
						rules = append(rules, CompiledRule{
							WorkloadID: local.WorkloadID,
							Direction:  "ingress",
							FromCIDR:   cidr,
							Port:       &p,
							Protocol:   proto,
							Action:     "allow",
							Reason:     "explicit-policy",
						})
					}
				}
			}

			for _, eg := range pol.Spec.Egress {
				hasExplicitEgress = true
				peers := resolveEgressPeers(eg.To, byDatabase, byQueue, byApp, local)
				for _, peer := range peers {
					if !sameEnvironment(local, peer) {
						continue
					}
					if peer.Address == "" {
						continue
					}
					cidr := hostCIDR(peer.Address)
					if len(eg.Ports) == 0 {
						rules = append(rules, CompiledRule{
							WorkloadID: local.WorkloadID,
							Direction:  "egress",
							ToCIDR:     cidr,
							Action:     "allow",
							Reason:     "explicit-policy",
						})
						continue
					}
					for _, port := range eg.Ports {
						p := port.Port
						proto := strings.ToLower(port.Protocol)
						if proto == "" {
							proto = "tcp"
						}
						rules = append(rules, CompiledRule{
							WorkloadID: local.WorkloadID,
							Direction:  "egress",
							ToCIDR:     cidr,
							Port:       &p,
							Protocol:   proto,
							Action:     "allow",
							Reason:     "explicit-policy",
						})
					}
				}
			}
		}

		// Unmatched traffic: explicit direction → allowlist deny catch-all;
		// otherwise environment default.
		if hasExplicitIngress {
			rules = append(rules, CompiledRule{
				WorkloadID: local.WorkloadID,
				Direction:  "ingress",
				FromCIDR:   "0.0.0.0/0",
				Action:     "deny",
				Reason:     "policy-default-deny",
			})
		} else if envDefault == DefaultDenyAll {
			rules = append(rules, CompiledRule{
				WorkloadID: local.WorkloadID,
				Direction:  "ingress",
				FromCIDR:   "0.0.0.0/0",
				Action:     "deny",
				Reason:     "default-deny-environment",
			})
		}

		if hasExplicitEgress {
			rules = append(rules, CompiledRule{
				WorkloadID: local.WorkloadID,
				Direction:  "egress",
				ToCIDR:     "0.0.0.0/0",
				Action:     "deny",
				Reason:     "policy-default-deny",
			})
		} else if envDefault == DefaultDenyAll {
			rules = append(rules, CompiledRule{
				WorkloadID: local.WorkloadID,
				Direction:  "egress",
				ToCIDR:     "0.0.0.0/0",
				Action:     "deny",
				Reason:     "default-deny-environment",
			})
		}
	}

	return NodeRuleSet{
		NodeID:     nodeID,
		Generation: generation,
		Rules:      rules,
	}
}

func sameEnvironment(a, b WorkloadPlacement) bool {
	return a.Organization == b.Organization &&
		a.Project == b.Project &&
		a.Environment == b.Environment
}

func hostCIDR(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.Contains(addr, "/") {
		return addr
	}
	return addr + "/32"
}

func indexPlacementsByApp(ps []WorkloadPlacement) map[string][]WorkloadPlacement {
	out := map[string][]WorkloadPlacement{}
	for _, p := range ps {
		if p.Application != nil && *p.Application != "" {
			k := envNameKey(p, *p.Application)
			out[k] = append(out[k], p)
		}
	}
	return out
}

func indexPlacementsByService(ps []WorkloadPlacement) map[string][]WorkloadPlacement {
	out := map[string][]WorkloadPlacement{}
	seen := map[string]map[string]bool{} // envNameKey → workload_id
	add := func(name string, p WorkloadPlacement) {
		if name == "" {
			return
		}
		k := envNameKey(p, name)
		if seen[k] == nil {
			seen[k] = map[string]bool{}
		}
		if seen[k][p.WorkloadID] {
			return
		}
		seen[k][p.WorkloadID] = true
		out[k] = append(out[k], p)
	}
	for _, p := range ps {
		if p.Service != nil {
			add(*p.Service, p)
		}
		// Applications are also addressable as services of the same name.
		if p.Application != nil {
			add(*p.Application, p)
		}
	}
	return out
}

func indexPlacementsByDatabase(ps []WorkloadPlacement) map[string][]WorkloadPlacement {
	out := map[string][]WorkloadPlacement{}
	for _, p := range ps {
		if p.Database != nil && *p.Database != "" {
			k := envNameKey(p, *p.Database)
			out[k] = append(out[k], p)
		}
	}
	return out
}

func indexPlacementsByQueue(ps []WorkloadPlacement) map[string][]WorkloadPlacement {
	out := map[string][]WorkloadPlacement{}
	for _, p := range ps {
		if p.Queue != nil && *p.Queue != "" {
			k := envNameKey(p, *p.Queue)
			out[k] = append(out[k], p)
		}
	}
	return out
}

func envNameKey(p WorkloadPlacement, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s", p.Organization, p.Project, p.Environment, name)
}

func resolveIngressPeers(from PeerRef, byService map[string][]WorkloadPlacement, local WorkloadPlacement) []WorkloadPlacement {
	if from.Service == "" {
		return nil
	}
	k := envNameKey(local, from.Service)
	return byService[k]
}

func resolveEgressPeers(
	to PeerRef,
	byDatabase, byQueue, byApp map[string][]WorkloadPlacement,
	local WorkloadPlacement,
) []WorkloadPlacement {
	switch {
	case to.Database != "":
		return byDatabase[envNameKey(local, to.Database)]
	case to.Queue != "":
		return byQueue[envNameKey(local, to.Queue)]
	case to.Service != "":
		// Service egress may target an application of the same name.
		if peers := byApp[envNameKey(local, to.Service)]; len(peers) > 0 {
			return peers
		}
		return nil
	default:
		return nil
	}
}
