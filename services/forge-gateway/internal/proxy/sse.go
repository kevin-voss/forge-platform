package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/middleware"
)

// IsSSERequest reports whether the client is requesting an SSE stream.
func IsSSERequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return IsSSEContentType(r.Header.Get("Accept"))
}

// IsSSEContentType reports whether ct looks like text/event-stream (Accept or Content-Type).
func IsSSEContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	// Accept may be a list; Content-Type may include parameters.
	for _, part := range strings.Split(ct, ",") {
		media := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if media == "text/event-stream" {
			return true
		}
	}
	return false
}

func (h *Handler) serveSSE(w http.ResponseWriter, r *http.Request, upstream *url.URL, originalHost, requestID string) {
	start := time.Now()
	upstreamURL := upstream.String()

	outReq := cloneOutboundRequest(r, upstream)
	middleware.StripHopByHop(outReq.Header)
	middleware.ApplyForwardedFrom(outReq, r, middleware.ForwardedOptions{
		TrustInboundXFF: h.cfg.TrustInboundXFF,
	})
	if requestID != "" {
		outReq.Header.Set(h.cfg.RequestIDHeader, requestID)
	}
	if outReq.Header.Get("Accept") == "" {
		outReq.Header.Set("Accept", "text/event-stream")
	}

	resp, err := h.transport.RoundTrip(outReq)
	if err != nil {
		if h.tracker != nil {
			h.tracker.RecordPassiveFailure(upstreamURL)
		}
		if isTimeout(err) {
			h.log.Warn("sse upstream timeout",
				"requestId", requestID,
				"host", originalHost,
				"path", r.URL.Path,
				"upstream", upstreamURL,
				"error", err.Error(),
			)
			httperr.Write(w, http.StatusGatewayTimeout, "gateway_timeout", "upstream request timed out")
			return
		}
		h.log.Warn("sse upstream error",
			"requestId", requestID,
			"host", originalHost,
			"path", r.URL.Path,
			"upstream", upstreamURL,
			"error", err.Error(),
		)
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "upstream connection error")
		return
	}
	defer resp.Body.Close()

	if h.tracker != nil {
		if resp.StatusCode >= 500 {
			h.tracker.RecordPassiveFailure(upstreamURL)
		} else {
			h.tracker.RecordPassiveSuccess(upstreamURL)
		}
	}

	copyHeader(w.Header(), resp.Header)
	if requestID != "" {
		w.Header().Set(h.cfg.RequestIDHeader, requestID)
	}
	if ct := resp.Header.Get("Content-Type"); IsSSEContentType(ct) {
		w.Header().Set("Content-Type", ct)
	} else if resp.StatusCode == http.StatusOK {
		w.Header().Set("Content-Type", "text/event-stream")
	}

	w.WriteHeader(resp.StatusCode)

	h.log.Info("sse open",
		"requestId", requestID,
		"host", originalHost,
		"path", r.URL.Path,
		"upstream", upstreamURL,
		"status", resp.StatusCode,
	)

	idle := h.cfg.WSIdleTimeout
	if idle <= 0 {
		idle = h.cfg.StreamReadTimeout
	}
	body := io.Reader(resp.Body)
	if idle > 0 {
		body = &idleReader{r: resp.Body, idle: idle, onIdle: func() { _ = resp.Body.Close() }}
	}

	bytesWritten, flushCount, streamErr := flushCopy(w, body, r.Context())
	reason := "closed"
	if streamErr != nil {
		switch {
		case errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded):
			reason = streamErr.Error()
		case isNetTimeout(streamErr):
			reason = "idle_timeout"
		case streamErr != io.EOF && !isClosedConn(streamErr):
			reason = streamErr.Error()
		}
	}

	h.log.Info("sse close",
		"requestId", requestID,
		"host", originalHost,
		"path", r.URL.Path,
		"upstream", upstreamURL,
		"duration_ms", time.Since(start).Milliseconds(),
		"bytes", bytesWritten,
		"flushes", flushCount,
		"reason", reason,
	)
}

// flushCopy copies src to w, flushing after every successful write.
func flushCopy(w http.ResponseWriter, src io.Reader, ctx context.Context) (int64, int, error) {
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	var written int64
	var flushes int

	for {
		if err := ctx.Err(); err != nil {
			return written, flushes, err
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if canFlush {
				flusher.Flush()
				flushes++
			}
			if ew != nil {
				return written, flushes, ew
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, flushes, nil
			}
			return written, flushes, er
		}
	}
}

// idleReader wraps a reader with an idle timeout between successful reads.
type idleReader struct {
	r      io.Reader
	idle   time.Duration
	onIdle func()

	mu    sync.Mutex
	timer *time.Timer
}

func (ir *idleReader) Read(p []byte) (int, error) {
	ir.mu.Lock()
	if ir.timer == nil {
		ir.timer = time.AfterFunc(ir.idle, func() {
			if ir.onIdle != nil {
				ir.onIdle()
			}
		})
	} else {
		ir.timer.Reset(ir.idle)
	}
	ir.mu.Unlock()

	n, err := ir.r.Read(p)
	if n > 0 {
		ir.mu.Lock()
		if ir.timer != nil {
			ir.timer.Reset(ir.idle)
		}
		ir.mu.Unlock()
	}
	if err != nil {
		ir.mu.Lock()
		if ir.timer != nil {
			ir.timer.Stop()
		}
		ir.mu.Unlock()
	}
	return n, err
}

// flushWriter counts Flush calls for unit tests.
type flushWriter struct {
	http.ResponseWriter
	flushes int
	writes  int
}

func (f *flushWriter) Write(p []byte) (int, error) {
	f.writes++
	return f.ResponseWriter.Write(p)
}

func (f *flushWriter) Flush() {
	f.flushes++
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
