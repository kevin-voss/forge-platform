package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type server struct {
	cfg       config
	log       *slog.Logger
	startedAt time.Time
}

type healthResponse struct {
	Status string `json:"status"`
}

type identityResponse struct {
	Service        string  `json:"service"`
	Language       string  `json:"language"`
	Status         string  `json:"status"`
	Version        string  `json:"version,omitempty"`
	UptimeSeconds  float64 `json:"uptime_seconds,omitempty"`
}

func newServer(cfg config, log *slog.Logger) *server {
	return &server{
		cfg:       cfg,
		log:       log,
		startedAt: time.Now().UTC(),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /{$}", s.handleIdentity)
	return mux
}

func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *server) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *server) handleIdentity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, identityResponse{
		Service:       s.cfg.ServiceName,
		Language:      "go",
		Status:        "running",
		Version:       s.cfg.ServiceVersion,
		UptimeSeconds: time.Since(s.startedAt).Seconds(),
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
