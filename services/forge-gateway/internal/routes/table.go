package routes

import (
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
)

// Table is a concurrency-safe in-memory route snapshot.
// Updates replace the entire snapshot atomically.
type Table struct {
	snapshot atomic.Pointer[[]Route]
}

// NewTable returns an empty route table.
func NewTable() *Table {
	t := &Table{}
	empty := []Route{}
	t.snapshot.Store(&empty)
	return t
}

// Snapshot returns a copy of the current routes.
func (t *Table) Snapshot() []Route {
	cur := t.snapshot.Load()
	if cur == nil || len(*cur) == 0 {
		return []Route{}
	}
	out := make([]Route, len(*cur))
	copy(out, *cur)
	return out
}

// Replace atomically swaps the route table with a validated, normalized snapshot.
func (t *Table) Replace(routes []Route) error {
	if err := Validate(routes); err != nil {
		return err
	}
	normalized := make([]Route, len(routes))
	for i, r := range routes {
		normalized[i] = r.Normalized()
	}
	t.snapshot.Store(&normalized)
	return nil
}

// Match looks up the best route for host+path against the current snapshot.
func (t *Table) Match(host, path string) (Route, bool) {
	cur := t.snapshot.Load()
	if cur == nil {
		return Route{}, false
	}
	return Match(*cur, host, path)
}

// Len returns the number of routes in the current snapshot.
func (t *Table) Len() int {
	cur := t.snapshot.Load()
	if cur == nil {
		return 0
	}
	return len(*cur)
}

// LoadFile reads a JSON route array from path and replaces the table.
func (t *Table) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read static routes: %w", err)
	}
	var routes []Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return fmt.Errorf("parse static routes: %w", err)
	}
	if err := t.Replace(routes); err != nil {
		return fmt.Errorf("load static routes: %w", err)
	}
	return nil
}
