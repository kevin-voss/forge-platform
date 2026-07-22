package logbuf_test

import (
	"sync"
	"testing"
	"time"

	"forge.local/services/forge-build/internal/logbuf"
)

func TestFanOutToMultipleSubscribers(t *testing.T) {
	buf := logbuf.New(100)
	buf.Append("one")

	ch1, unsub1 := buf.Subscribe()
	defer unsub1()
	ch2, unsub2 := buf.Subscribe()
	defer unsub2()

	mustRecv(t, ch1, "one")
	mustRecv(t, ch2, "one")

	buf.Append("two")
	mustRecv(t, ch1, "two")
	mustRecv(t, ch2, "two")

	buf.Close()
	waitClosed(t, ch1)
	waitClosed(t, ch2)

	snap := buf.Snapshot()
	if len(snap) != 2 || snap[0] != "one" || snap[1] != "two" {
		t.Fatalf("snapshot = %#v", snap)
	}
}

func TestBufferBound(t *testing.T) {
	buf := logbuf.New(2)
	buf.Append("a")
	buf.Append("b")
	buf.Append("c")
	snap := buf.Snapshot()
	if len(snap) != 2 || snap[0] != "b" || snap[1] != "c" {
		t.Fatalf("snapshot = %#v", snap)
	}
}

func mustRecv(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed, want %q", want)
		}
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %q", want)
	}
}

func waitClosed(t *testing.T, ch <-chan string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close")
		}
	}
}

func TestConcurrentAppend(t *testing.T) {
	buf := logbuf.New(1000)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			buf.Append("line")
		}(i)
	}
	wg.Wait()
	if len(buf.Snapshot()) != 50 {
		t.Fatalf("len=%d", len(buf.Snapshot()))
	}
}
