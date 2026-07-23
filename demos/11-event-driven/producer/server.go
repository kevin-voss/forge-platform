package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type server struct {
	cfg       config
	log       *slog.Logger
	client    *http.Client
	startedAt time.Time
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

type publishResult struct {
	EventID string `json:"event_id"`
	Stream  string `json:"stream"`
	Seq     uint64 `json:"seq"`
	Status  int    `json:"status"`
}

type validPublishResponse struct {
	Count   int             `json:"count"`
	Events  []publishResult `json:"events"`
	IdemKey string          `json:"idempotency_key,omitempty"`
}

type singlePublishResponse struct {
	Event   publishResult   `json:"event,omitempty"`
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body,omitempty"`
	IdemKey string          `json:"idempotency_key,omitempty"`
}

func newServer(cfg config, log *slog.Logger) *server {
	return &server{
		cfg: cfg,
		log: log,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		startedAt: time.Now().UTC(),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /{$}", s.handleIdentity)
	mux.HandleFunc("POST /v1/publish/valid", s.handlePublishValid)
	mux.HandleFunc("POST /v1/publish/malformed", s.handlePublishMalformed)
	mux.HandleFunc("POST /v1/publish/poison", s.handlePublishPoison)
	mux.HandleFunc("POST /v1/publish/duplicate", s.handlePublishDuplicate)
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

func (s *server) handlePublishValid(w http.ResponseWriter, r *http.Request) {
	count := s.cfg.DefaultCount
	if raw := r.URL.Query().Get("count"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "count must be 1–50"})
			return
		}
		count = n
	}

	results := make([]publishResult, 0, count)
	for i := 0; i < count; i++ {
		payload := ValidCrashPayload(fmt.Sprintf("demo-api-%d", i+1), "oom", i+1)
		idemKey := ""
		if i == 0 {
			idemKey = s.cfg.IdempotencyKey
		}
		res, err := s.publish(payload, idemKey)
		if err != nil {
			s.log.Error("publish valid failed", "index", i, "error", err.Error())
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if res.Status != http.StatusAccepted {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":  "unexpected publish status",
				"status": res.Status,
				"body":   json.RawMessage(res.rawBody),
			})
			return
		}
		results = append(results, res.result)
	}

	writeJSON(w, http.StatusOK, validPublishResponse{
		Count:   len(results),
		Events:  results,
		IdemKey: s.cfg.IdempotencyKey,
	})
}

func (s *server) handlePublishMalformed(w http.ResponseWriter, _ *http.Request) {
	body, err := json.Marshal(publishBody{
		Subject: subjectApplicationCrashed,
		Data:    MalformedCrashPayload(),
		Source:  "demo-events-producer",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	status, respBody, err := s.postEvents(body, "")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, singlePublishResponse{
		Status: status,
		Body:   respBody,
	})
}

func (s *server) handlePublishPoison(w http.ResponseWriter, _ *http.Request) {
	res, err := s.publish(PoisonCrashPayload(), "")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, singlePublishResponse{
		Event:  res.result,
		Status: res.Status,
		Body:   json.RawMessage(res.rawBody),
	})
}

func (s *server) handlePublishDuplicate(w http.ResponseWriter, _ *http.Request) {
	payload := ValidCrashPayload("demo-api-1", "oom", 1)
	res, err := s.publish(payload, s.cfg.IdempotencyKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, singlePublishResponse{
		Event:   res.result,
		Status:  res.Status,
		Body:    json.RawMessage(res.rawBody),
		IdemKey: s.cfg.IdempotencyKey,
	})
}

type publishOutcome struct {
	Status  int
	result  publishResult
	rawBody []byte
}

func (s *server) publish(data any, idemKey string) (publishOutcome, error) {
	body, err := encodePublishBody(data)
	if err != nil {
		return publishOutcome{}, err
	}
	status, respBody, err := s.postEvents(body, idemKey)
	if err != nil {
		return publishOutcome{}, err
	}
	out := publishOutcome{Status: status, rawBody: respBody}
	if status == http.StatusAccepted {
		var parsed publishResult
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return out, fmt.Errorf("decode publish response: %w", err)
		}
		parsed.Status = status
		out.result = parsed
	}
	return out, nil
}

func (s *server) postEvents(body []byte, idemKey string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, s.cfg.EventsURL+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
