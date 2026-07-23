package dns

import (
	"testing"
	"time"
)

func TestAnswerTTLCapsAndTracksLease(t *testing.T) {
	p := TTLPolicy{MaxTTL: 5 * time.Second, NegativeTTL: 2 * time.Second}
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

	if got := p.AnswerTTL(now.Add(30*time.Second), now); got != 5 {
		t.Fatalf("long lease: got %d want 5", got)
	}
	if got := p.AnswerTTL(now.Add(3*time.Second), now); got != 3 {
		t.Fatalf("short lease: got %d want 3", got)
	}
	if got := p.AnswerTTL(now.Add(500*time.Millisecond), now); got != 1 {
		t.Fatalf("sub-second remaining: got %d want 1", got)
	}
	if got := p.AnswerTTL(now.Add(-time.Second), now); got != 1 {
		t.Fatalf("expired: got %d want 1", got)
	}
	if got := p.NegTTL(); got != 2 {
		t.Fatalf("NegTTL = %d", got)
	}
}
