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
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("openapi missing %q", needle)
		}
	}
}
