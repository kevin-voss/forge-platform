package proxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/middleware"
)

// IsWebSocketUpgrade reports whether r is a WebSocket upgrade request.
func IsWebSocketUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	return headerHasToken(r.Header, "Connection", "upgrade")
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func (h *Handler) serveWebSocket(w http.ResponseWriter, r *http.Request, upstream *url.URL, originalHost, requestID string) {
	start := time.Now()
	upstreamURL := upstream.String()

	hj, ok := w.(http.Hijacker)
	if !ok {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", "hijacking not supported")
		return
	}

	dialer := net.Dialer{Timeout: h.cfg.ConnectTimeout}
	upConn, err := dialer.DialContext(r.Context(), "tcp", hostPort(upstream))
	if err != nil {
		if h.tracker != nil {
			h.tracker.RecordPassiveFailure(upstreamURL)
		}
		h.log.Warn("websocket upstream dial failed",
			"requestId", requestID,
			"host", originalHost,
			"path", r.URL.Path,
			"upstream", upstreamURL,
			"error", err.Error(),
		)
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "upstream connection error")
		return
	}

	outReq := cloneOutboundRequest(r, upstream)
	prepareWebSocketHeaders(outReq, r, h.cfg, requestID)

	if h.cfg.ResponseHeaderTimeout > 0 {
		_ = upConn.SetDeadline(time.Now().Add(h.cfg.ResponseHeaderTimeout))
	}
	if err := outReq.Write(upConn); err != nil {
		_ = upConn.Close()
		if h.tracker != nil {
			h.tracker.RecordPassiveFailure(upstreamURL)
		}
		h.log.Warn("websocket upgrade write failed",
			"requestId", requestID,
			"upstream", upstreamURL,
			"error", err.Error(),
		)
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "upstream connection error")
		return
	}

	br := bufio.NewReader(upConn)
	upResp, err := http.ReadResponse(br, outReq)
	_ = upConn.SetDeadline(time.Time{}) // clear header deadline before streaming
	if err != nil {
		_ = upConn.Close()
		if h.tracker != nil {
			h.tracker.RecordPassiveFailure(upstreamURL)
		}
		h.log.Warn("websocket upgrade read failed",
			"requestId", requestID,
			"upstream", upstreamURL,
			"error", err.Error(),
		)
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "upstream connection error")
		return
	}

	if upResp.StatusCode != http.StatusSwitchingProtocols {
		_ = upResp.Body.Close()
		_ = upConn.Close()
		if h.tracker != nil {
			h.tracker.RecordPassiveFailure(upstreamURL)
		}
		h.log.Warn("websocket upgrade refused",
			"requestId", requestID,
			"upstream", upstreamURL,
			"status", upResp.StatusCode,
		)
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "upstream refused websocket upgrade")
		return
	}

	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		_ = upResp.Body.Close()
		_ = upConn.Close()
		h.log.Warn("websocket hijack failed",
			"requestId", requestID,
			"error", err.Error(),
		)
		return
	}
	_ = upResp.Body.Close()

	if requestID != "" {
		upResp.Header.Set(h.cfg.RequestIDHeader, requestID)
	}
	if err := upResp.Write(clientBuf); err != nil {
		_ = clientConn.Close()
		_ = upConn.Close()
		h.log.Warn("websocket client write failed",
			"requestId", requestID,
			"error", err.Error(),
		)
		return
	}
	if err := clientBuf.Flush(); err != nil {
		_ = clientConn.Close()
		_ = upConn.Close()
		h.log.Warn("websocket client flush failed",
			"requestId", requestID,
			"error", err.Error(),
		)
		return
	}

	if h.tracker != nil {
		h.tracker.RecordPassiveSuccess(upstreamURL)
	}

	h.log.Info("websocket open",
		"requestId", requestID,
		"host", originalHost,
		"path", r.URL.Path,
		"upstream", upstreamURL,
	)

	var bytesClientToUp, bytesUpToClient atomic.Int64
	errCh := make(chan copyResult, 2)

	go func() {
		n, copyErr := copyWithIdleTimeout(upConn, clientConn, clientConn, h.cfg.WSIdleTimeout, h.cfg.StreamReadTimeout)
		bytesClientToUp.Store(n)
		errCh <- copyResult{err: copyErr, dir: "client_to_upstream"}
	}()
	go func() {
		// br retains any bytes buffered past the 101 response.
		n, copyErr := copyWithIdleTimeout(clientConn, br, upConn, h.cfg.WSIdleTimeout, h.cfg.StreamReadTimeout)
		bytesUpToClient.Store(n)
		errCh <- copyResult{err: copyErr, dir: "upstream_to_client"}
	}()

	first := <-errCh
	_ = clientConn.Close()
	_ = upConn.Close()
	second := <-errCh

	reason := closeReason(first, second)
	h.log.Info("websocket close",
		"requestId", requestID,
		"host", originalHost,
		"path", r.URL.Path,
		"upstream", upstreamURL,
		"duration_ms", time.Since(start).Milliseconds(),
		"bytes_client_to_upstream", bytesClientToUp.Load(),
		"bytes_upstream_to_client", bytesUpToClient.Load(),
		"reason", reason,
	)
}

type copyResult struct {
	err error
	dir string
}

func closeReason(a, b copyResult) string {
	for _, r := range []copyResult{a, b} {
		if r.err == nil {
			continue
		}
		if isNetTimeout(r.err) {
			return "idle_timeout"
		}
		if r.err != io.EOF && !isClosedConn(r.err) {
			return r.dir + ": " + r.err.Error()
		}
	}
	return "closed"
}

func isNetTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isClosedConn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset")
}

func prepareWebSocketHeaders(out, in *http.Request, cfg Config, requestID string) {
	middleware.StripHopByHop(out.Header)
	out.Header.Set("Connection", "Upgrade")
	out.Header.Set("Upgrade", "websocket")
	middleware.ApplyForwardedFrom(out, in, middleware.ForwardedOptions{
		TrustInboundXFF: cfg.TrustInboundXFF,
	})
	if requestID != "" {
		out.Header.Set(cfg.RequestIDHeader, requestID)
	}
}

func cloneOutboundRequest(r *http.Request, upstream *url.URL) *http.Request {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	out.URL = &url.URL{
		Scheme:   upstream.Scheme,
		Host:     upstream.Host,
		Path:     path,
		RawPath:  r.URL.RawPath,
		RawQuery: r.URL.RawQuery,
	}
	if upstream.Path != "" && upstream.Path != "/" {
		out.URL.Path = singleJoiningSlash(upstream.Path, path)
	}
	out.Host = upstream.Host
	out.URL.Host = upstream.Host
	out.URL.Scheme = upstream.Scheme
	return out
}

func hostPort(u *url.URL) string {
	host := u.Host
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(host, port)
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

// copyWithIdleTimeout copies src→dst, refreshing read deadlines from deadlineConn.
// When idle > 0, each Read must complete within idle. When readTimeout > 0 and idle
// is 0, readTimeout is used as a fixed per-read deadline. 0/0 clears deadlines.
func copyWithIdleTimeout(dst io.Writer, src io.Reader, deadlineConn net.Conn, idle, readTimeout time.Duration) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if deadlineConn != nil {
			switch {
			case idle > 0:
				_ = deadlineConn.SetReadDeadline(time.Now().Add(idle))
			case readTimeout > 0:
				_ = deadlineConn.SetReadDeadline(time.Now().Add(readTimeout))
			default:
				_ = deadlineConn.SetReadDeadline(time.Time{})
			}
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}
