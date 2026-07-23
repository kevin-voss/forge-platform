package observability

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Middleware extracts/creates trace context, ensures request ids, metrics, logs.
func Middleware(p *Provider, log *slog.Logger) func(http.Handler) http.Handler {
	propagator := otel.GetTextMapPropagator()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			reqID := resolveRequestID(r)
			r.Header.Set(HeaderForgeRequestID, reqID)
			r.Header.Set(HeaderLegacyRequestID, reqID)
			w.Header().Set(HeaderForgeRequestID, reqID)
			w.Header().Set(HeaderLegacyRequestID, reqID)
			ctx = ContextWithRequestID(ctx, reqID)

			tracer := otel.Tracer("forge.build")
			if p != nil && p.Tracer != nil {
				tracer = p.Tracer
			}
			ctx, span := tracer.Start(ctx, "HTTP "+r.Method,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
					attribute.String("forge.service", "forge-build"),
					attribute.String("request_id", reqID),
				),
			)
			defer span.End()
			propagator.Inject(ctx, propagation.HeaderCarrier(r.Header))

			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r.WithContext(ctx))

			status := rw.status
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			if !isHealthPath(r.URL.Path) {
				p.RecordHTTP(ctx, r.Method, status, time.Since(start).Seconds())
				if log != nil {
					sc := span.SpanContext()
					attrs := []any{
						"method", r.Method,
						"path", r.URL.Path,
						"status", status,
						"duration_ms", time.Since(start).Milliseconds(),
						"request_id", reqID,
						"forge.service", "forge-build",
					}
					if sc.IsValid() {
						attrs = append(attrs,
							"trace_id", sc.TraceID().String(),
							"span_id", sc.SpanID().String(),
						)
					}
					log.Info("request", attrs...)
				}
			}
		})
	}
}

// InjectHeaders writes traceparent + request ids onto an outbound request.
func InjectHeaders(r *http.Request) {
	if r == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(r.Context(), propagation.HeaderCarrier(r.Header))
	if id := RequestIDFromContext(r.Context()); id != "" {
		r.Header.Set(HeaderForgeRequestID, id)
		r.Header.Set(HeaderLegacyRequestID, id)
	}
}

func resolveRequestID(r *http.Request) string {
	for _, h := range []string{HeaderForgeRequestID, HeaderLegacyRequestID} {
		if id := r.Header.Get(h); validRequestID(id) {
			return id
		}
	}
	return NewRequestID()
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

func isHealthPath(path string) bool {
	return path == "/health" || path == "/health/live" || path == "/health/ready" ||
		(len(path) >= 8 && path[:8] == "/health/")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
