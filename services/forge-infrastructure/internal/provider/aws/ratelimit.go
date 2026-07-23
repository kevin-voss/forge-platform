package aws

import (
	"context"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter is a header-driven token bucket with 429 backoff-and-jitter.
type Limiter struct {
	mu          sync.Mutex
	remaining   int
	resetAt     time.Time
	maxInFlight int
	inFlight    int
	consecutive int
	minBackoff  time.Duration
	maxBackoff  time.Duration
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	lastDelay   time.Duration
}

// NewLimiter constructs a Limiter with a concurrent-in-flight cap.
func NewLimiter(maxConcurrent int) *Limiter {
	if maxConcurrent < 1 {
		maxConcurrent = 5
	}
	return &Limiter{
		remaining:   1000,
		maxInFlight: maxConcurrent,
		minBackoff:  200 * time.Millisecond,
		maxBackoff:  30 * time.Second,
		now:         time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

// Acquire waits until an in-flight slot is available and rate-limit remaining > 0.
func (l *Limiter) Acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.now()
		if !l.resetAt.IsZero() && now.After(l.resetAt) {
			l.remaining = 1000
			l.resetAt = time.Time{}
			l.consecutive = 0
		}
		if l.inFlight < l.maxInFlight && l.remaining > 0 {
			l.inFlight++
			if l.remaining > 0 {
				l.remaining--
			}
			l.mu.Unlock()
			return nil
		}
		wait := l.minBackoff
		if !l.resetAt.IsZero() {
			if d := l.resetAt.Sub(now); d > wait {
				wait = d
			}
		}
		if wait > l.maxBackoff {
			wait = l.maxBackoff
		}
		l.lastDelay = wait
		l.mu.Unlock()
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// Release frees one in-flight slot.
func (l *Limiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight > 0 {
		l.inFlight--
	}
}

// ObserveHeaders updates remaining/reset from Retry-After / x-amzn-RequestLimit-* headers.
func (l *Limiter) ObserveHeaders(h http.Header) {
	if h == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if v := h.Get("x-amzn-RequestLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			l.remaining = n
		}
	}
	if v := h.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil {
			l.resetAt = l.now().Add(time.Duration(sec) * time.Second)
		}
	}
}

// Backoff429 computes the next delay for a 429, updates consecutive count, and sleeps.
func (l *Limiter) Backoff429(ctx context.Context, h http.Header) error {
	l.mu.Lock()
	l.consecutive++
	n := l.consecutive
	base := float64(l.minBackoff) * math.Pow(2, float64(n-1))
	if base > float64(l.maxBackoff) {
		base = float64(l.maxBackoff)
	}
	jitter := rand.Float64() * 0.25 * base
	delay := time.Duration(base + jitter)
	if h != nil {
		if v := h.Get("Retry-After"); v != "" {
			if sec, err := strconv.Atoi(v); err == nil {
				until := time.Duration(sec) * time.Second
				if until > delay {
					delay = until
				}
			}
		}
	}
	if delay > l.maxBackoff {
		delay = l.maxBackoff
	}
	l.lastDelay = delay
	l.mu.Unlock()
	return l.sleep(ctx, delay)
}

// LastDelay returns the most recent backoff/wait delay (tests).
func (l *Limiter) LastDelay() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastDelay
}

// ResetSuccess clears consecutive 429 tracking after a successful call.
func (l *Limiter) ResetSuccess() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consecutive = 0
}
