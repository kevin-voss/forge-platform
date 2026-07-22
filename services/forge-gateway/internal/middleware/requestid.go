package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
)

const DefaultRequestIDHeader = "X-Request-Id"

type requestIDContextKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// NewRequestID generates a correlation id aligned with Control's req_… form.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_00000000000000000000000000000000"
	}
	return "req_" + hex.EncodeToString(b[:])
}

// RequestIDFromContext returns the request id stored by RequestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(requestIDContextKey{}).(string)
	return v
}

// ContextWithRequestID stores a request id on ctx.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

// RequestID returns middleware that reuses or generates a request id, stores it
// on the request context, sets it on the inbound request, and echoes it on the response.
func RequestID(header string) func(http.Handler) http.Handler {
	if header == "" {
		header = DefaultRequestIDHeader
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(header)
			if !requestIDPattern.MatchString(id) {
				id = NewRequestID()
			}
			r.Header.Set(header, id)
			w.Header().Set(header, id)
			ctx := ContextWithRequestID(r.Context(), id)
			next.ServeHTTP(&requestIDWriter{ResponseWriter: w, header: header, id: id}, r.WithContext(ctx))
		})
	}
}

// requestIDWriter ensures the request-id header survives upstream response copies.
type requestIDWriter struct {
	http.ResponseWriter
	header string
	id     string
}

func (w *requestIDWriter) WriteHeader(statusCode int) {
	w.Header().Set(w.header, w.id)
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *requestIDWriter) Write(b []byte) (int, error) {
	// Ensure header is present even if WriteHeader was skipped.
	w.Header().Set(w.header, w.id)
	return w.ResponseWriter.Write(b)
}
