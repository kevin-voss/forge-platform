package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-network/internal/api"
	"forge.local/services/forge-network/internal/db"
	"forge.local/services/forge-network/internal/network"
	"forge.local/services/forge-network/internal/policy"
)

func TestNetworkPolicyReadyAndDenyRules(t *testing.T) {
	srv, netName, cleanup := startPolicyTestServer(t)
	defer cleanup()

	mustLease(t, srv, netName, "node-c")
	mustWorkloadLease(t, srv, netName, "node-c", "wl_restricted")
	mustWorkloadLease(t, srv, netName, "node-c", "wl_allowed")
	mustUpsertPlacement(t, srv, "wl_restricted", `{
		"organization":"default","project":"demo","environment":"production",
		"node_id":"node-c","application":"restricted"
	}`)
	mustUpsertPlacement(t, srv, "wl_allowed", `{
		"organization":"default","project":"demo","environment":"production",
		"node_id":"node-c","application":"allowed-caller","service":"allowed-caller"
	}`)

	body := `{
		"name":"restricted-policy",
		"spec":{
			"target":{"application":"restricted"},
			"ingress":[{"from":{"service":"allowed-caller"},"ports":[{"port":8080,"protocol":"tcp"}]}]
		}
	}`
	resp, err := http.Post(srv.URL+"/v1/projects/demo/environments/production/network-policies",
		"application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"phase":"Ready"`) {
		t.Fatalf("expected Ready: %s", raw)
	}

	rulesResp, err := http.Get(srv.URL + "/v1/nodes/node-c/network-policy-rules")
	if err != nil {
		t.Fatal(err)
	}
	defer rulesResp.Body.Close()
	var rs policy.NodeRuleSet
	if err := json.NewDecoder(rulesResp.Body).Decode(&rs); err != nil {
		t.Fatal(err)
	}
	var deny bool
	var allow bool
	for _, r := range rs.Rules {
		if r.Action == "deny" {
			deny = true
		}
		if r.Action == "allow" && r.Direction == "ingress" {
			allow = true
		}
	}
	if !deny {
		t.Fatalf("expected deny rule for unmatched traffic: %+v", rs.Rules)
	}
	if !allow {
		t.Fatalf("expected allow for allowed-caller: %+v", rs.Rules)
	}
}

func TestEnvironmentDefaultDenyAllClosesPaths(t *testing.T) {
	srv, netName, cleanup := startPolicyTestServer(t)
	defer cleanup()

	mustLease(t, srv, netName, "node-a")
	mustWorkloadLease(t, srv, netName, "node-a", "wl_api")
	mustUpsertPlacement(t, srv, "wl_api", `{
		"organization":"default","project":"demo","environment":"production",
		"node_id":"node-a","application":"invoice-api"
	}`)

	// Baseline: allow-within → no default-deny rules.
	rs := mustGetRules(t, srv, "node-a")
	for _, r := range rs.Rules {
		if r.Reason == "default-deny-environment" {
			t.Fatalf("unexpected default deny under allow-within: %+v", r)
		}
	}

	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL+"/v1/projects/demo/environments/production/network-defaults",
		bytes.NewBufferString(`{"defaultPolicy":"deny-all"}`))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("patch defaults status=%d body=%s", resp.StatusCode, b)
	}

	rs = mustGetRules(t, srv, "node-a")
	var closed bool
	for _, r := range rs.Rules {
		if r.Action == "deny" && r.Reason == "default-deny-environment" {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("deny-all should close previously-open paths: %+v", rs.Rules)
	}
}

func TestDeniedConnectionMetricAndEvent(t *testing.T) {
	srv, _, cleanup := startPolicyTestServer(t)
	defer cleanup()

	resp, err := http.Post(srv.URL+"/v1/nodes/node-c/network-policy-denied",
		"application/json",
		bytes.NewBufferString(`{"from_workload":"wl_x","to_workload":"wl_restricted","port":8080,"protocol":"tcp"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "network.policy.denied") {
		t.Fatalf("expected platform event acknowledgement: %s", raw)
	}

	mresp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	mraw, _ := io.ReadAll(mresp.Body)
	if !strings.Contains(string(mraw), "forge_network_policy_denied_total") {
		t.Fatalf("metrics missing deny counter: %s", mraw)
	}
	if !strings.Contains(string(mraw), "forge_network_policy_denied_total 1") {
		t.Fatalf("expected denied_total=1: %s", mraw)
	}
}

func startPolicyTestServer(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	dsn := os.Getenv("FORGE_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://forge:forge@127.0.0.1:5001/forge?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	database, err := db.Open(ctx, dsn, "network", 4, true)
	cancel()
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}

	alloc := &network.Allocator{Pool: database.Pool, SkipDocker: true}
	reg := &network.PeerRegistry{Pool: database.Pool, KeepaliveSeconds: 25, MTU: 1420, Metrics: &network.PeerMetrics{}}
	mem := &network.MembershipStore{Pool: database.Pool, DefaultMode: network.TransportWireguard, Metrics: &network.TransportMetrics{}}
	comp := &network.PeerSetComputer{Registry: reg, Membership: mem}
	pol := &policy.Store{Pool: database.Pool, ClusterDefault: policy.DefaultAllowWithin}
	compiler := &policy.PolicyCompiler{ClusterDefault: policy.DefaultAllowWithin}
	metrics := &api.PolicyMetrics{}
	mux := api.NewRouter(api.Deps{
		Alloc: alloc, Registry: reg, Computer: comp, Membership: mem,
		Policy: pol, Compiler: compiler, PolicyMetrics: metrics, DB: database,
	})
	srv := httptest.NewServer(mux)

	ctx2 := context.Background()
	name := "itest-policy-" + time.Now().UTC().Format("150405.000")
	if _, err := alloc.CreateNetwork(ctx2, name, "10.150.0.0/16", 24, nil); err != nil {
		srv.Close()
		database.Close()
		t.Fatalf("create network: %v", err)
	}

	cleanup := func() {
		_, _ = database.Pool.Exec(context.Background(),
			`DELETE FROM network.network_policies WHERE project='demo'`)
		_, _ = database.Pool.Exec(context.Background(),
			`DELETE FROM network.environment_network_defaults WHERE project='demo'`)
		_, _ = database.Pool.Exec(context.Background(),
			`DELETE FROM network.workload_placements WHERE project='demo'`)
		_, _ = database.Pool.Exec(context.Background(),
			`DELETE FROM network.workload_leases WHERE network_id IN (SELECT id FROM network.networks WHERE name=$1)`, name)
		_, _ = database.Pool.Exec(context.Background(),
			`DELETE FROM network.node_leases WHERE network_id IN (SELECT id FROM network.networks WHERE name=$1)`, name)
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM network.networks WHERE name=$1`, name)
		srv.Close()
		database.Close()
	}
	return srv, name, cleanup
}

func mustWorkloadLease(t *testing.T, srv *httptest.Server, netName, nodeID, wl string) {
	t.Helper()
	body := `{"node_id":"` + nodeID + `","workload_id":"` + wl + `"}`
	resp, err := http.Post(srv.URL+"/v1/networks/"+netName+"/workload-leases",
		"application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("workload lease status=%d body=%s", resp.StatusCode, b)
	}
}

func mustUpsertPlacement(t *testing.T, srv *httptest.Server, wl, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/workload-placements/"+wl, bytes.NewBufferString(body))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("placement status=%d body=%s", resp.StatusCode, b)
	}
}

func mustGetRules(t *testing.T, srv *httptest.Server, nodeID string) policy.NodeRuleSet {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/nodes/" + nodeID + "/network-policy-rules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rs policy.NodeRuleSet
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		t.Fatal(err)
	}
	return rs
}
