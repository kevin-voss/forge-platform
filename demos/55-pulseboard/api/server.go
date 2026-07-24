package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// Stats is the live dashboard payload sourced from Forge Observe (55.04).
type Stats struct {
	Replicas  int     `json:"replicas"`
	Counter   int64   `json:"counter"`
	RPS       float64 `json:"rps"`
	P95Ms     float64 `json:"p95Ms"`
	Instance  string  `json:"instance"`
	Source    string  `json:"source"`
	UpdatedAt string  `json:"updatedAt"`
}

type server struct {
	counter  atomic.Int64
	instance string
	replicas int // fallback when Observe is unavailable
	observe  *ObserveClient
	otel     *otelHandle
	log      *slog.Logger
}

func newServer(observe *ObserveClient, otelH *otelHandle, log *slog.Logger) *server {
	instance := os.Getenv("HOSTNAME")
	if instance == "" {
		instance = "pulseboard-api"
	}
	replicas := 1
	if raw := os.Getenv("PULSEBOARD_REPLICAS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			replicas = n
		}
	}
	if log == nil {
		log = slog.Default()
	}
	return &server{
		instance: instance,
		replicas: replicas,
		observe:  observe,
		otel:     otelH,
		log:      log,
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("POST /hit", s.handleHit)
	mux.HandleFunc("POST /counter", s.handleHit)
	var h http.Handler = withCORS(mux)
	if s.otel != nil {
		h = s.otel.Middleware(h)
	}
	return h
}

func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.counter.Add(1)
	stats := Stats{
		Replicas:  s.replicas,
		Counter:   s.counter.Load(),
		RPS:       0,
		P95Ms:     0,
		Instance:  s.instance,
		Source:    "local",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	if s.observe != nil && s.observe.Enabled() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		platform, err := s.observe.FetchPlatformStats(ctx)
		if err != nil {
			s.log.Warn("observe stats query failed; using local fallback",
				"error", err.Error(),
				"span", "pulseboard.stats.observe",
			)
			if s.otel != nil {
				if p95 := s.otel.LocalP95Seconds(); p95 > 0 {
					stats.P95Ms = p95 * 1000
				}
			}
		} else {
			stats.Replicas = platform.Replicas
			stats.RPS = platform.RPS
			stats.P95Ms = platform.P95Ms
			stats.Source = platform.Source
		}
	} else if s.otel != nil {
		if p95 := s.otel.LocalP95Seconds(); p95 > 0 {
			stats.P95Ms = p95 * 1000
		}
	}

	writeJSON(w, http.StatusOK, stats)
}

func (s *server) handleHit(w http.ResponseWriter, _ *http.Request) {
	n := s.counter.Add(1)
	writeJSON(w, http.StatusOK, map[string]any{
		"counter": n,
		"status":  "ok",
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}
