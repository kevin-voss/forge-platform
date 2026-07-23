package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
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
	otel      *otelHandle
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

func newServer(cfg config, log *slog.Logger, otelProvider *otelHandle) *server {
	return &server{
		cfg:       cfg,
		log:       log,
		otel:      otelProvider,
		startedAt: time.Now().UTC(),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /{$}", s.handleIdentity)
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

		reqID := r.Header.Get("X-Forge-Request-ID")
		if reqID == "" {
			reqID = r.Header.Get("X-Request-Id")
		}
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
		if reqID != "" {
			attrs = append(attrs, "request_id", reqID)
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
