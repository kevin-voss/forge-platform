package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type server struct {
	cfg       config
	log       *slog.Logger
	events    eventsPublisher
	store     incidentStore
	identity  *identityClient
	storage   *storageClient
	otel      *otelHandle
	startedAt time.Time
}

type healthResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type identityResponse struct {
	Service       string  `json:"service"`
	Language      string  `json:"language"`
	Status        string  `json:"status"`
	Version       string  `json:"version,omitempty"`
	UptimeSeconds float64 `json:"uptime_seconds,omitempty"`
	DBBackend     string  `json:"db_backend,omitempty"`
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

func newServer(cfg config, log *slog.Logger, store incidentStore, otelProvider *otelHandle) *server {
	var pub eventsPublisher
	if cfg.EventsURL != "" {
		pub = newHTTPEventsPublisher(cfg.EventsURL, cfg.ServiceName)
	}
	var idClient *identityClient
	if cfg.ProductAuth == "enforce" {
		idClient = newIdentityClient(cfg.IdentityURL, cfg.ProjectID)
	}
	var stor *storageClient
	if cfg.StorageURL != "" && cfg.StorageProject != "" {
		stor = newStorageClient(cfg.StorageURL, cfg.StorageProject, cfg.StorageBucket)
	}
	return &server{
		cfg:       cfg,
		log:       log,
		events:    pub,
		store:     store,
		identity:  idClient,
		storage:   stor,
		otel:      otelProvider,
		startedAt: time.Now().UTC(),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /{$}", s.handleIdentity)
	mux.HandleFunc("GET /db-status", s.handleDBStatus)
	mux.HandleFunc("GET /secret-status", s.handleSecretStatus)
	mux.HandleFunc("POST /incidents", s.requireAuth(s.handleCreateIncident))
	mux.HandleFunc("GET /incidents", s.requireAuth(s.handleListIncidents))
	mux.HandleFunc("GET /incidents/{id}", s.requireAuth(s.handleGetIncident))
	mux.HandleFunc("POST /artifacts", s.requireAuth(s.handleUploadArtifact))
	mux.HandleFunc("GET /artifacts/{key}", s.requireAuth(s.handleGetArtifact))
	return s.withTrace(mux)
}

func (s *server) withTrace(next http.Handler) http.Handler {
	propagator := otel.GetTextMapPropagator()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		tracer := s.otel.tracer
		ctx, span := tracer.Start(ctx, "HTTP "+r.Method,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("forge.service", s.cfg.ServiceName),
			),
		)
		defer span.End()

		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r.WithContext(ctx))
		span.SetAttributes(attribute.Int("http.response.status_code", rw.status))
		if rw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(rw.status))
		}

		if r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" {
			return
		}
		sc := span.SpanContext()
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"forge.service", s.cfg.ServiceName,
		}
		if sc.IsValid() {
			attrs = append(attrs,
				"trace_id", sc.TraceID().String(),
				"span_id", sc.SpanID().String(),
			)
		}
		s.log.Info("request", attrs...)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "not_ready", Error: "database"})
		return
	}
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *server) handleIdentity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, identityResponse{
		Service:       s.cfg.ServiceName,
		Language:      "go",
		Status:        "running",
		Version:       s.cfg.ServiceVersion,
		UptimeSeconds: time.Since(s.startedAt).Seconds(),
		DBBackend:     s.store.Backend(),
	})
}

func (s *server) handleDBStatus(w http.ResponseWriter, _ *http.Request) {
	present := s.cfg.DatabaseURL != ""
	payload := map[string]any{
		"DATABASE_URL_present": present,
		"backend":              s.store.Backend(),
	}
	blob, _ := json.Marshal(payload)
	if present && strings.Contains(string(blob), s.cfg.DatabaseURL) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "leak"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *server) handleSecretStatus(w http.ResponseWriter, _ *http.Request) {
	present := s.cfg.AppSharedSecret != ""
	payload := map[string]any{
		"APP_SHARED_SECRET_present": present,
		"value_length":              len(s.cfg.AppSharedSecret),
		"PRODUCT_MODE":              s.cfg.ProductMode,
		"PRODUCT_MODE_present":      s.cfg.ProductMode != "",
	}
	blob, _ := json.Marshal(payload)
	if present && strings.Contains(string(blob), s.cfg.AppSharedSecret) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "leak"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
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

	if err := s.store.Create(r.Context(), inc); err != nil {
		s.log.Error("incident persist failed", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist_failed"})
		return
	}

	s.log.Info("incident created", "incident_id", inc.ID, "severity", inc.Severity, "backend", s.store.Backend())
	if s.events != nil {
		if err := s.events.PublishIncidentCreated(inc); err != nil {
			s.log.Error("incident.created publish failed", "incident_id", inc.ID, "error", err.Error())
		} else {
			s.log.Info("incident.created published", "incident_id", inc.ID)
		}
	}
	writeJSON(w, http.StatusCreated, inc)
}

func (s *server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inc, ok, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get_failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

func (s *server) handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil || !s.storage.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		key = "log-bundle-" + newID()[:12] + ".txt"
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read_failed"})
		return
	}
	if len(data) == 0 {
		data = []byte("capstone artifact\n")
	}
	if err := s.storage.EnsureBucket(r.Context()); err != nil {
		s.log.Error("storage ensure bucket failed", "error", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "storage_bucket_failed"})
		return
	}
	digest, err := s.storage.PutObject(r.Context(), key, data)
	if err != nil {
		s.log.Error("storage put failed", "error", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "storage_put_failed"})
		return
	}
	s.log.Info("artifact stored", "key", key, "sha256", digest, "bytes", len(data))
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":    key,
		"bucket": s.cfg.StorageBucket,
		"sha256": digest,
		"bytes":  len(data),
	})
}

func (s *server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil || !s.storage.enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage_unavailable"})
		return
	}
	key := r.PathValue("key")
	data, err := s.storage.GetObject(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
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
