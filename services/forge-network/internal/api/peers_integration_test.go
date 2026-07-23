package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"forge.local/services/forge-network/internal/api"
	"forge.local/services/forge-network/internal/db"
	"forge.local/services/forge-network/internal/network"
)

func TestThreeNodesPeerDistribution(t *testing.T) {
	srv, name, cleanup := startPeerTestServer(t)
	defer cleanup()

	for i, node := range []string{"node-a", "node-b", "node-c"} {
		mustLease(t, srv, name, node)
		mustRegister(t, srv, name, node, "b64:k"+node, "203.0.113."+itoa(i+1)+":51820")
	}

	for _, node := range []string{"node-a", "node-b", "node-c"} {
		ps := mustGetPeers(t, srv, name, node)
		if len(ps.Peers) != 2 {
			t.Fatalf("%s peers=%d", node, len(ps.Peers))
		}
		seen := map[string]string{}
		for _, p := range ps.Peers {
			seen[p.NodeID] = p.AllowedIPs[0]
		}
		if node != "node-a" && seen["node-a"] == "" {
			t.Fatalf("%s missing node-a", node)
		}
	}
}

func TestFourthJoinIncrementsOnlyOnline(t *testing.T) {
	srv, name, cleanup := startPeerTestServer(t)
	defer cleanup()

	for _, node := range []string{"node-a", "node-b", "node-c"} {
		mustLease(t, srv, name, node)
		mustRegister(t, srv, name, node, "b64:"+node, "1.2.3.4:51820")
	}
	// Simulate offline by leaving via lease release after marking — use registry SetOnline via leave path:
	// release node-c lease triggers OnLeave → offline.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/networks/"+name+"/node-leases/node-c", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("release status=%d", resp.StatusCode)
	}

	beforeA := mustGetPeers(t, srv, name, "node-a").PeerVersion
	beforeB := mustGetPeers(t, srv, name, "node-b").PeerVersion

	mustLease(t, srv, name, "node-d")
	mustRegister(t, srv, name, "node-d", "b64:node-d", "5.6.7.8:51820")

	afterA := mustGetPeers(t, srv, name, "node-a").PeerVersion
	afterB := mustGetPeers(t, srv, name, "node-b").PeerVersion
	if afterA <= beforeA || afterB <= beforeB {
		t.Fatalf("expected online bumps a:%d→%d b:%d→%d", beforeA, afterA, beforeB, afterB)
	}
	psD := mustGetPeers(t, srv, name, "node-d")
	if len(psD.Peers) != 2 {
		t.Fatalf("node-d should see a,b only; peers=%d", len(psD.Peers))
	}
}

func TestRotateKeyUnbrokenDualWindow(t *testing.T) {
	srv, name, cleanup := startPeerTestServer(t)
	defer cleanup()

	mustLease(t, srv, name, "node-a")
	mustLease(t, srv, name, "node-b")
	mustRegister(t, srv, name, "node-a", "b64:old-a", "198.51.100.1:51820")
	mustRegister(t, srv, name, "node-b", "b64:b", "198.51.100.2:51820")

	body := `{"new_public_key":"b64:rotated-a"}`
	resp, err := http.Post(srv.URL+"/v1/networks/"+name+"/nodes/node-a/rotate-key",
		"application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("rotate status=%d body=%s", resp.StatusCode, b)
	}
	var rot network.RotateKeyResult
	if err := json.NewDecoder(resp.Body).Decode(&rot); err != nil {
		t.Fatal(err)
	}
	if rot.Status != "rotating" {
		t.Fatalf("status=%s", rot.Status)
	}

	ps := mustGetPeers(t, srv, name, "node-b")
	keys := 0
	for _, p := range ps.Peers {
		if p.NodeID == "node-a" {
			keys++
		}
	}
	if keys != 2 {
		t.Fatalf("expected 2 keys for node-a during rotation, got %d", keys)
	}

	resp2, err := http.Post(srv.URL+"/v1/networks/"+name+"/nodes/node-a/retire-key",
		"application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("retire status=%d", resp2.StatusCode)
	}
	ps2 := mustGetPeers(t, srv, name, "node-b")
	keys = 0
	for _, p := range ps2.Peers {
		if p.NodeID == "node-a" {
			keys++
			if p.PublicKey != "b64:rotated-a" {
				t.Fatalf("key=%s", p.PublicKey)
			}
		}
	}
	if keys != 1 {
		t.Fatalf("after retire expected 1 key, got %d", keys)
	}
}

func startPeerTestServer(t *testing.T) (*httptest.Server, string, func()) {
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
	comp := &network.PeerSetComputer{Registry: reg}
	mux := api.NewRouter(api.Deps{Alloc: alloc, Registry: reg, Computer: comp, DB: database})
	srv := httptest.NewServer(mux)

	ctx2 := context.Background()
	name := "itest-peers-" + time.Now().UTC().Format("150405.000")
	row, err := alloc.CreateNetwork(ctx2, name, "10.130.0.0/16", 24, nil)
	if err != nil {
		srv.Close()
		database.Close()
		t.Fatalf("create network: %v", err)
	}

	cleanup := func() {
		srv.Close()
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.wireguard_peers WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.node_leases WHERE network_id=$1`, row.ID)
		_, _ = database.Pool.Exec(ctx2, `DELETE FROM network.networks WHERE id=$1`, row.ID)
		database.Close()
	}
	return srv, name, cleanup
}

func mustLease(t *testing.T, srv *httptest.Server, name, node string) {
	t.Helper()
	body := `{"node_id":"` + node + `"}`
	resp, err := http.Post(srv.URL+"/v1/networks/"+name+"/node-leases", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("lease %s: %d %s", node, resp.StatusCode, b)
	}
}

func mustRegister(t *testing.T, srv *httptest.Server, name, node, key, endpoint string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"public_key": key, "endpoint": endpoint})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/networks/"+name+"/nodes/"+node+"/wireguard", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("register %s: %d %s", node, resp.StatusCode, b)
	}
}

func mustGetPeers(t *testing.T, srv *httptest.Server, name, node string) network.PeerSetResponse {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1/networks/" + name + "/nodes/" + node + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("peers %s: %d %s", node, resp.StatusCode, b)
	}
	var ps network.PeerSetResponse
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		t.Fatal(err)
	}
	return ps
}

func itoa(n int) string {
	return string(rune('0' + n))
}
