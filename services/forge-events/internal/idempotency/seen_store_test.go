package idempotency

import (
	"context"
	"testing"
	"time"
)

func TestNormalizeIdempotencyKey(t *testing.T) {
	got, err := NormalizeIdempotencyKey("  k-123  ")
	if err != nil || got != "k-123" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := NormalizeIdempotencyKey("bad key"); err == nil {
		t.Fatal("expected whitespace error")
	}
	long := make([]byte, MaxIdempotencyKeyLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NormalizeIdempotencyKey(string(long)); err == nil {
		t.Fatal("expected length error")
	}
}

func TestMemorySeenStoreMarkIdempotent(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	if err := s.Mark(ctx, "c1", "evt_1"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if err := s.Mark(ctx, "c1", "evt_1"); err != nil {
		t.Fatalf("duplicate Mark: %v", err)
	}
	ok, err := s.IsProcessed(ctx, "c1", "evt_1")
	if err != nil || !ok {
		t.Fatalf("IsProcessed = %v, %v", ok, err)
	}
	ok, err = s.IsProcessed(ctx, "c2", "evt_1")
	if err != nil || ok {
		t.Fatalf("cross-consumer IsProcessed = %v, %v", ok, err)
	}
}

func TestMemorySeenStoreCleanup(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.Mark(ctx, "c1", "old")
	s.mu.Lock()
	s.seen[memKey("c1", "old")] = time.Now().UTC().Add(-2 * time.Hour)
	s.mu.Unlock()
	_ = s.Mark(ctx, "c1", "new")
	n, err := s.Cleanup(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted = %d, want 1", n)
	}
	ok, _ := s.IsProcessed(ctx, "c1", "old")
	if ok {
		t.Fatal("old marker should be gone")
	}
	ok, _ = s.IsProcessed(ctx, "c1", "new")
	if !ok {
		t.Fatal("new marker should remain")
	}
}
