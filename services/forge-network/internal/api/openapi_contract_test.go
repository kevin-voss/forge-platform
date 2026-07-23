package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIDeclaresNetworkAndLeases(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	yamlPath := filepath.Join(root, "contracts/openapi/forge-network.openapi.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Skipf("openapi not in build context: %v", err)
	}
	text := string(raw)
	for _, needle := range []string{
		"/health/live:",
		"/health/ready:",
		"getHealthLive",
		"getHealthReady",
		"/v1/networks:",
		"/v1/networks/{name}:",
		"createNetwork",
		"getNetwork",
		"listNetworks",
		"node-leases",
		"workload-leases",
		"allocateNodeLease",
		"releaseNodeLease",
		"allocateWorkloadLease",
		"releaseWorkloadLease",
		"clusterCidr",
		"nodePrefixLength",
		"Network",
		"NodeLease",
		"WorkloadLease",
		"NoAddressSpaceAvailable",
		"NodeBlockExhausted",
		"CidrCollision",
		"10.100.0.0/16",
		"node-a",
		"wl_123",
		"/v1/networks/{name}/nodes/{node_id}/peers",
		"/v1/networks/{name}/nodes/{node_id}/rotate-key",
		"/v1/networks/{name}/nodes/{node_id}/applied-version",
		"getNodePeers",
		"rotateNodeKey",
		"reportAppliedPeerVersion",
		"registerWireGuardPeer",
		"PeerSetResponse",
		"RotateKeyRequest",
		"applied_peer_version",
		"persistent_keepalive",
		"b64:rotated...",
		"/v1/nodes/{id}/network-membership",
		"/v1/networks/{name}/transport",
		"patchNodeNetworkMembership",
		"getNetworkTransport",
		"provider-private",
		"hetzner-private-fsn1",
		"NetworkMembership",
		"TransportPair",
		"docker_colocated",
		"/v1/projects/{project}/environments/{environment}/network-policies",
		"/v1/projects/{project}/environments/{environment}/network-defaults",
		"/v1/nodes/{node_id}/network-policy-rules",
		"createNetworkPolicy",
		"listNetworkPolicies",
		"getNetworkPolicy",
		"patchNetworkDefaults",
		"getNetworkPolicyRules",
		"NetworkPolicy",
		"NetworkPolicySpec",
		"NetworkPolicyRules",
		"EnvironmentNetworkDefaults",
		"invoice-frontend",
		"invoice-api",
		"default-deny-environment",
		"forge_network_policy_denied_total",
		"network.policy.denied",
		"listWorkloadLeases",
		"reportRouteDrift",
		"/v1/networks/{name}/route-drift",
		"drift_count",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("openapi missing %q", needle)
		}
	}
}
