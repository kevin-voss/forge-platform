package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"forge.local/services/forge-observe/internal/identity"
	"forge.local/services/forge-observe/internal/logs"
)

// LogsStreamHandler serves GET /v1/logs/stream (SSE).
type LogsStreamHandler struct {
	Service *logs.Service
	Caps    logs.Caps
	Auth    *identity.Gate
	Log     *slog.Logger
	Now     func() time.Time
	Opts    logs.TailOptions

	active atomic.Int64
}

// Register mounts the SSE log stream route.
func (h *LogsStreamHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/logs/stream", h.handleStream)
}

func (h *LogsStreamHandler) handleStream(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	caps := h.Caps
	if caps.MaxLimit == 0 {
		caps = logs.DefaultCaps()
	}

	q := r.URL.Query()
	f, err := logs.ValidateAndNormalize(
		q.Get("project"),
		q.Get("deployment"),
		q.Get("service"),
		q.Get("request_id"),
		q.Get("trace_id"),
		q.Get("q"),
		q.Get("since"),
		"", // until = now (live)
		"100",
		"forward",
		"",
		now,
		caps,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filters", err.Error(), nil)
		return
	}

	principal, err := h.Auth.Authenticate(r)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication failed", nil)
		return
	}

	allowed, err := h.Auth.AuthorizeProject(r.Context(), principal, f.Project)
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			writeError(w, http.StatusForbidden, "forbidden", "project log access denied", map[string]string{
				"project": f.Project,
			})
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", err.Error(), nil)
		return
	}

	if h.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "loki_unavailable", "log stream service not configured", nil)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "response writer does not support flushing", nil)
		return
	}

	// Probe Loki once before committing to SSE so the CLI can fall back on 503.
	probeCtx, cancelProbe := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancelProbe()
	if _, err := h.Service.Query(probeCtx, f); err != nil {
		if errors.Is(err, logs.ErrLokiUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "loki_unavailable", "Loki is unavailable; try again later", nil)
			return
		}
		writeError(w, http.StatusBadRequest, "query_failed", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	h.active.Add(1)
	defer h.active.Add(-1)

	opts := h.Opts
	if opts.PollInterval <= 0 {
		opts = logs.DefaultTailOptions()
	}

	ctx := r.Context()
	err = h.Service.Tail(ctx, f, opts, func(e logs.Entry) error {
		if allowed != nil {
			filtered := logs.FilterByProjects([]logs.Entry{e}, allowed, false)
			if len(filtered) == 0 {
				return nil
			}
			e = filtered[0]
		}
		payload, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		// Stream already started; write a terminal SSE error event if possible.
		_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonQuote(err.Error()))
		flusher.Flush()
		if h.Log != nil {
			h.Log.Warn("log stream ended with error", "error", err.Error(), "span", "observe.logs.stream")
		}
	}
}

func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"error"`
	}
	return string(b)
}
