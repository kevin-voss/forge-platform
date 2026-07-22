// Package logbuf provides a bounded build-log buffer with live fan-out.
package logbuf

import (
	"sync"
)

// Buffer stores log lines and fans them out to live subscribers.
type Buffer struct {
	mu     sync.Mutex
	lines  []string
	max    int
	closed bool
	subs   map[*subscriber]struct{}
}

type subscriber struct {
	ch     chan string
	done   chan struct{}
	once   sync.Once
	closed bool
}

// New creates a buffer that retains at most maxLines (oldest dropped).
func New(maxLines int) *Buffer {
	if maxLines < 1 {
		maxLines = 1
	}
	return &Buffer{
		max:  maxLines,
		subs: make(map[*subscriber]struct{}),
	}
}

// Append records a line and notifies subscribers.
func (b *Buffer) Append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	for sub := range b.subs {
		select {
		case sub.ch <- line:
		default:
			// Drop for slow subscribers; Snapshot still has history.
		}
	}
}

// Snapshot returns a copy of retained lines.
func (b *Buffer) Snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

// Subscribe returns a channel of historical then live lines, plus an unsubscribe func.
// When the buffer is Closed, the channel is closed after draining.
func (b *Buffer) Subscribe() (<-chan string, func()) {
	sub := &subscriber{
		ch:   make(chan string, 128),
		done: make(chan struct{}),
	}

	b.mu.Lock()
	hist := make([]string, len(b.lines))
	copy(hist, b.lines)
	alreadyClosed := b.closed
	if !alreadyClosed {
		b.subs[sub] = struct{}{}
	}
	b.mu.Unlock()

	unsub := func() {
		sub.once.Do(func() {
			close(sub.done)
			b.mu.Lock()
			if _, ok := b.subs[sub]; ok {
				delete(b.subs, sub)
			}
			if !sub.closed {
				sub.closed = true
				close(sub.ch)
			}
			b.mu.Unlock()
		})
	}

	go func() {
		defer unsub()
		for _, line := range hist {
			select {
			case <-sub.done:
				return
			case sub.ch <- line:
			}
		}
		if alreadyClosed {
			return
		}
		<-sub.done
	}()

	return sub.ch, unsub
}

// Close marks the buffer finished and closes all live subscriber channels.
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for sub := range b.subs {
		delete(b.subs, sub)
		sub.once.Do(func() {
			close(sub.done)
			if !sub.closed {
				sub.closed = true
				close(sub.ch)
			}
		})
	}
}

// Closed reports whether Close has been called.
func (b *Buffer) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}
