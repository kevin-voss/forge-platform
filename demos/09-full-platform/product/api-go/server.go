package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type server struct {
	cfg       config
	log       *slog.Logger
	startedAt time.Time
	mu        sync.RWMutex
	incidents map[string]incident
}

type healthResponse struct {
	Status string `json:"status"`
}

type identityResponse struct {
	Service       string  `json:"service"`
	Language      string  `json:"language"`
	Status        string  `json:"status"`
	Version       string  `json:"version,omitempty"`
	UptimeSeconds float64 `json:"uptime_seconds,omitempty"`
}

type incident struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Severity    string    `json:"severity"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type createIncidentRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

func newServer(cfg config, log *slog.Logger) *server {
	return &server{
		cfg:       cfg,
		log:       log,
		startedAt: time.Now().UTC(),
		incidents: make(map[string]incident),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /{$}", s.handleIdentity)
	mux.HandleFunc("POST /incidents", s.handleCreateIncident)
	mux.HandleFunc("GET /incidents", s.handleListIncidents)
	mux.HandleFunc("GET /incidents/{id}", s.handleGetIncident)
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

func (s *server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var req createIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if req.Title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title_required"})
		return
	}
	severity := req.Severity
	if severity == "" {
		severity = "medium"
	}

	inc := incident{
		ID:          newID(),
		Title:       req.Title,
		Description: req.Description,
		Severity:    severity,
		Status:      "open",
		CreatedAt:   time.Now().UTC(),
	}

	s.mu.Lock()
	s.incidents[inc.ID] = inc
	s.mu.Unlock()

	s.log.Info("incident created", "incident_id", inc.ID, "severity", inc.Severity)
	writeJSON(w, http.StatusCreated, inc)
}

func (s *server) handleListIncidents(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	out := make([]incident, 0, len(s.incidents))
	for _, inc := range s.incidents {
		out = append(out, inc)
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	inc, ok := s.incidents[id]
	s.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
