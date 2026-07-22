package routes

import (
	"sync"
	"testing"
)

func TestTableReplaceAndSnapshot(t *testing.T) {
	table := NewTable()
	if err := table.Replace([]Route{
		{Host: "a.localhost", Upstreams: []Upstream{{URL: "http://127.0.0.1:1"}}, Strategy: StrategyRoundRobin},
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	snap := table.Snapshot()
	if len(snap) != 1 || snap[0].Host != "a.localhost" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Mutating snapshot must not affect table.
	snap[0].Host = "mutated"
	if table.Snapshot()[0].Host != "a.localhost" {
		t.Fatal("snapshot must be a copy")
	}
}

func TestTableReplaceValidation(t *testing.T) {
	table := NewTable()
	err := table.Replace([]Route{
		{Host: "a.localhost", Upstreams: []Upstream{{URL: "ftp://bad"}}, Strategy: StrategyRoundRobin},
	})
	if err == nil {
		t.Fatal("expected validation error for bad upstream scheme")
	}
	if table.Len() != 0 {
		t.Fatal("failed replace must not mutate table")
	}
}

func TestTableConcurrentReplaceAndMatch(t *testing.T) {
	table := NewTable()
	routesA := []Route{{Host: "a.localhost", Upstreams: []Upstream{{URL: "http://127.0.0.1:1"}}, Strategy: StrategyRoundRobin}}
	routesB := []Route{{Host: "b.localhost", Upstreams: []Upstream{{URL: "http://127.0.0.1:2"}}, Strategy: StrategyRoundRobin}}
	if err := table.Replace(routesA); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = table.Replace(routesA)
			_ = table.Replace(routesB)
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = table.Match("a.localhost", "/")
				_, _ = table.Match("b.localhost", "/")
			}
		}()
	}
	wg.Wait()

	// Final table must be a valid snapshot of A or B.
	snap := table.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("len=%d, want 1", len(snap))
	}
	host := snap[0].Host
	if host != "a.localhost" && host != "b.localhost" {
		t.Fatalf("unexpected host %q", host)
	}
}
