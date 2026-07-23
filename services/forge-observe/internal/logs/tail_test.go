package logs_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"forge.local/services/forge-observe/internal/logs"
)

type seqQuerier struct {
	mu    sync.Mutex
	calls int
	pages [][]logs.StreamValue
	err   error
}

func (q *seqQuerier) QueryRange(_ context.Context, _ string, start, end time.Time, _ int, _ string) ([]logs.StreamValue, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return nil, q.err
	}
	idx := q.calls
	q.calls++
	if idx >= len(q.pages) {
		return nil, nil
	}
	var out []logs.StreamValue
	for _, sv := range q.pages[idx] {
		if !sv.Timestamp.Before(start) && sv.Timestamp.Before(end) || sv.Timestamp.Equal(end) {
			if sv.Timestamp.After(start) && !sv.Timestamp.After(end) {
				out = append(out, sv)
			}
		}
	}
	// Simpler: return page as-is; Tail filters by cursor.
	return q.pages[idx], nil
}

func TestTailEmitsAndAdvancesCursor(t *testing.T) {
	t1 := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(2 * time.Second)
	line := func(msg string) string {
		b, _ := json.Marshal(map[string]any{"message": msg, "service": "svc", "level": "info"})
		return string(b)
	}
	q := &seqQuerier{
		pages: [][]logs.StreamValue{
			{{Timestamp: t1, Line: line("a")}, {Timestamp: t2, Line: line("b")}},
			{}, // idle poll
		},
	}
	svc := &logs.Service{
		Loki: q,
		Now:  func() time.Time { return t2.Add(time.Second) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var got []string
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Tail(ctx, logs.Filters{
			Service:   "svc",
			Since:     t1.Add(-time.Second),
			Direction: logs.DirectionForward,
		}, logs.TailOptions{PollInterval: 20 * time.Millisecond, BatchLimit: 10}, func(e logs.Entry) error {
			got = append(got, e.Message)
			if len(got) >= 2 {
				cancel()
			}
			return nil
		})
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Tail: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got = %#v", got)
	}
}

func TestTailLokiUnavailable(t *testing.T) {
	svc := &logs.Service{Loki: &seqQuerier{err: errors.New("down")}}
	err := svc.Tail(context.Background(), logs.Filters{TraceID: "T"}, logs.DefaultTailOptions(), func(logs.Entry) error {
		return nil
	})
	if !errors.Is(err, logs.ErrLokiUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
