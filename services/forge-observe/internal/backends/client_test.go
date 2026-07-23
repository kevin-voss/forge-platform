package backends_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forge.local/services/forge-observe/internal/backends"
	"forge.local/services/forge-observe/internal/config"
)

func TestHealthyOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}))
	t.Cleanup(srv.Close)

	c := backends.NewLoki(srv.URL, backends.Options{Timeout: time.Second})
	if err := c.Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
}

func TestHealthyEnforcesDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := backends.NewTempo(srv.URL, backends.Options{Timeout: 50 * time.Millisecond})
	start := time.Now()
	err := c.Healthy(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("want deadline error, got %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("Healthy hung for %v; expected quick timeout", elapsed)
	}
}

func TestPrometheusHealthPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/healthy" {
			t.Fatalf("path = %q, want /-/healthy", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := backends.NewPrometheus(srv.URL, backends.Options{Timeout: time.Second})
	if err := c.Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
}

func TestRegistryReadyAndStatusMap(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ok.Close)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)

	reg := &backends.Registry{
		Loki:       backends.NewLoki(ok.URL, backends.Options{Timeout: time.Second}),
		Tempo:      backends.NewTempo(down.URL, backends.Options{Timeout: time.Second}),
		Prometheus: backends.NewPrometheus(ok.URL, backends.Options{Timeout: time.Second}),
		Required:   []config.BackendName{config.BackendLoki, config.BackendTempo, config.BackendPrometheus},
	}

	status := reg.StatusMap(context.Background())
	if status["loki"] != backends.StatusOK || status["prometheus"] != backends.StatusOK {
		t.Fatalf("status = %#v", status)
	}
	if status["tempo"] != backends.StatusDown {
		t.Fatalf("tempo status = %q, want down", status["tempo"])
	}
	if err := reg.ReadyError(); err == nil || !strings.Contains(err.Error(), "tempo") {
		t.Fatalf("ReadyError = %v, want tempo failure", err)
	}

	reg.Required = []config.BackendName{config.BackendLoki, config.BackendPrometheus}
	if err := reg.ReadyError(); err != nil {
		t.Fatalf("ReadyError with tempo optional: %v", err)
	}
}
