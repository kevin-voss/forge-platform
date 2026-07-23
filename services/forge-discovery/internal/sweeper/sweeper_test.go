package sweeper

import (
	"context"
	"testing"
	"time"

	"forge.local/services/forge-discovery/internal/store"
)

type memStore struct {
	expired []store.EndpointRow
	calls   int
	reaped  []store.EndpointRow
}

func (m *memStore) ExpireLeases(_ context.Context, _ time.Time) ([]store.EndpointRow, error) {
	m.calls++
	if m.calls == 1 {
		out := append([]store.EndpointRow{}, m.expired...)
		m.expired = nil
		return out, nil
	}
	return nil, nil
}

func (m *memStore) ReapUnready(_ context.Context, _ time.Time) ([]store.EndpointRow, error) {
	return m.reaped, nil
}

func TestSweepOnceExpiresOnce(t *testing.T) {
	st := &memStore{expired: []store.EndpointRow{{ID: "ep-1"}}}
	r := &Runner{
		Store: st,
		Cfg:   Config{Interval: time.Second, ReapAfter: time.Minute},
		Now:   func() time.Time { return time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC) },
	}
	r.SweepOnce(context.Background())
	if st.calls != 1 {
		t.Fatalf("calls = %d", st.calls)
	}
	r.SweepOnce(context.Background())
	if st.calls != 2 {
		t.Fatalf("second calls = %d", st.calls)
	}
}
