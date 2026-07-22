package proxy

import (
	"testing"

	"forge.local/services/forge-gateway/internal/routes"
)

func TestRoundRobinDistributes(t *testing.T) {
	rr := newRoundRobin([]routes.Upstream{
		{URL: "http://127.0.0.1:1"},
		{URL: "http://127.0.0.1:2"},
		{URL: "http://127.0.0.1:3"},
	})
	counts := map[string]int{}
	for i := 0; i < 30; i++ {
		u, err := rr.next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		counts[u.Host]++
	}
	if len(counts) != 3 {
		t.Fatalf("expected 3 hosts, got %v", counts)
	}
	for host, n := range counts {
		if n != 10 {
			t.Fatalf("host %s count=%d, want 10", host, n)
		}
	}
}
