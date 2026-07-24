package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// Stats is the live dashboard payload (Observe wiring lands in 55.04).
type Stats struct {
	Replicas  int    `json:"replicas"`
	Counter   int64  `json:"counter"`
	RPS       float64 `json:"rps"`
	P95Ms     float64 `json:"p95Ms"`
	Instance  string `json:"instance"`
	UpdatedAt string `json:"updatedAt"`
}

type server struct {
	counter  atomic.Int64
	instance string
	replicas int
}

func newServer() *server {
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
	return &server{instance: instance, replicas: replicas}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("POST /hit", s.handleHit)
	mux.HandleFunc("POST /counter", s.handleHit)
	return withCORS(mux)
}

func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleStats(w http.ResponseWriter, _ *http.Request) {
	// Count dashboard polls as light load so /stats stays useful under traffic.
	s.counter.Add(1)
	writeJSON(w, http.StatusOK, Stats{
		Replicas:  s.replicas,
		Counter:   s.counter.Load(),
		RPS:       0,
		P95Ms:     0,
		Instance:  s.instance,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
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
