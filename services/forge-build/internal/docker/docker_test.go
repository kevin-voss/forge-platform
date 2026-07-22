package docker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubEngine struct {
	pingErrs []error
	version  string
	verErr   error
	pings    int
}

func (s *stubEngine) Ping(context.Context) error {
	i := s.pings
	s.pings++
	if i < len(s.pingErrs) {
		return s.pingErrs[i]
	}
	if len(s.pingErrs) > 0 {
		return s.pingErrs[len(s.pingErrs)-1]
	}
	return nil
}

func (s *stubEngine) ServerVersion(context.Context) (string, error) {
	return s.version, s.verErr
}

func (s *stubEngine) Close() error { return nil }

func TestStartupPingSuccessAfterRetry(t *testing.T) {
	eng := &stubEngine{
		pingErrs: []error{errors.New("temporary"), nil},
		version:  "27.0.0",
	}
	ver, err := StartupPing(context.Background(), eng, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("StartupPing: %v", err)
	}
	if ver != "27.0.0" {
		t.Fatalf("version = %q", ver)
	}
	if eng.pings != 2 {
		t.Fatalf("pings = %d, want 2", eng.pings)
	}
}

func TestStartupPingExhausted(t *testing.T) {
	eng := &stubEngine{
		pingErrs: []error{errors.New("down")},
	}
	if _, err := StartupPing(context.Background(), eng, 1, 0); err == nil {
		t.Fatal("expected error")
	}
}
