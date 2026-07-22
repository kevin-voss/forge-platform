package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"forge.local/services/forge-gateway/internal/health"
	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/middleware"
	"forge.local/services/forge-gateway/internal/routes"
)

var (
	errNoUpstream        = errors.New("no upstream")
	errNoHealthyUpstream = errors.New("no healthy upstream")
)

// Config holds proxy timeout and header behavior.
type Config struct {
	RequestIDHeader       string
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	OverallTimeout        time.Duration
	TrustInboundXFF       bool
}

// Handler dispatches matched requests to upstreams via httputil.ReverseProxy.
type Handler struct {
	table     *routes.Table
	tracker   *health.UpstreamTracker
	log       *slog.Logger
	cfg       Config
	transport http.RoundTripper
	pickers   sync.Map // routeKey -> *roundRobin
}

// NewHandler returns a data-plane proxy handler bound to table.
// tracker may be nil (all upstreams treated as ready).
func NewHandler(table *routes.Table, log *slog.Logger, tracker *health.UpstreamTracker, cfg Config) *Handler {
	if log == nil {
		log = slog.Default()
	}
	if cfg.RequestIDHeader == "" {
		cfg.RequestIDHeader = middleware.DefaultRequestIDHeader
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   cfg.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &Handler{
		table:     table,
		tracker:   tracker,
		log:       log,
		cfg:       cfg,
		transport: transport,
	}
}

// ServeHTTP matches a route and reverse-proxies to a chosen upstream.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := h.table.Match(r.Host, r.URL.Path)
	if !ok {
		httperr.Write(w, http.StatusNotFound, "no_route", "no route matched host/path")
		return
	}

	upstream, err := h.pick(route)
	if err != nil {
		if errors.Is(err, errNoHealthyUpstream) {
			httperr.Write(w, http.StatusServiceUnavailable, "no_healthy_upstream", "no ready upstream available")
			return
		}
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "no usable upstream")
		return
	}

	req := r
	cancel := func() {}
	if !route.TimeoutExempt && h.cfg.OverallTimeout > 0 {
		var ctx context.Context
		ctx, cancel = context.WithTimeout(r.Context(), h.cfg.OverallTimeout)
		req = r.WithContext(ctx)
	}
	defer cancel()

	// Capture client view before Director rewrites Host/URL.
	originalHost := r.Host
	requestID := middleware.RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = r.Header.Get(h.cfg.RequestIDHeader)
	}

	start := time.Now()
	rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	upstreamURL := upstream.String()
	proxy := &httputil.ReverseProxy{
		Transport: h.transport,
		// Rewrite (not Director) so we fully own X-Forwarded-* without ReverseProxy appending again.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Host = upstream.Host
			middleware.StripHopByHop(pr.Out.Header)
			middleware.ApplyForwardedFrom(pr.Out, pr.In, middleware.ForwardedOptions{
				TrustInboundXFF: h.cfg.TrustInboundXFF,
			})
			if requestID != "" {
				pr.Out.Header.Set(h.cfg.RequestIDHeader, requestID)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if requestID != "" {
				resp.Header.Set(h.cfg.RequestIDHeader, requestID)
			}
			if h.tracker == nil {
				return nil
			}
			if resp.StatusCode >= 500 {
				h.tracker.RecordPassiveFailure(upstreamURL)
			} else {
				h.tracker.RecordPassiveSuccess(upstreamURL)
			}
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			if h.tracker != nil {
				h.tracker.RecordPassiveFailure(upstreamURL)
			}
			elapsed := time.Since(start)
			if isTimeout(err) {
				h.log.Warn("upstream timeout",
					"requestId", requestID,
					"host", originalHost,
					"path", req.URL.Path,
					"upstream", upstreamURL,
					"elapsed_ms", elapsed.Milliseconds(),
					"error", err.Error(),
				)
				httperr.Write(rw, http.StatusGatewayTimeout, "gateway_timeout", "upstream request timed out")
				return
			}
			h.log.Warn("upstream error",
				"requestId", requestID,
				"host", originalHost,
				"path", req.URL.Path,
				"upstream", upstreamURL,
				"error", err.Error(),
			)
			httperr.Write(rw, http.StatusBadGateway, "bad_gateway", "upstream connection error")
		},
	}
	proxy.ServeHTTP(rw, req)

	h.log.Info("proxied request",
		"requestId", requestID,
		"route_host", route.Host,
		"route_path_prefix", route.PathPrefix,
		"upstream", upstreamURL,
		"status", rw.status,
		"duration_ms", time.Since(start).Milliseconds(),
		"method", r.Method,
		"path", r.URL.Path,
		"host", originalHost,
	)
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

func (h *Handler) pick(route routes.Route) (*url.URL, error) {
	candidates := route.Upstreams
	if h.tracker != nil {
		candidates = h.tracker.FilterReady(route.Upstreams)
	}
	if len(candidates) == 0 {
		if len(route.Upstreams) == 0 {
			return nil, errNoUpstream
		}
		return nil, errNoHealthyUpstream
	}

	key := routeKey(route)
	v, _ := h.pickers.LoadOrStore(key, newRoundRobin(candidates))
	rr := v.(*roundRobin)
	// Rebuild when the ready set size diverges (health transitions or route replace).
	if rr.len() != len(candidates) {
		rr = newRoundRobin(candidates)
		h.pickers.Store(key, rr)
	}
	return rr.next()
}

// InvalidatePickers drops cached balancers (call after route replace).
func (h *Handler) InvalidatePickers() {
	h.pickers.Range(func(key, _ any) bool {
		h.pickers.Delete(key)
		return true
	})
}

// Tracker exposes the upstream health tracker (may be nil).
func (h *Handler) Tracker() *health.UpstreamTracker {
	return h.tracker
}

type roundRobin struct {
	urls []*url.URL
	idx  atomic.Uint64
}

func newRoundRobin(upstreams []routes.Upstream) *roundRobin {
	urls := make([]*url.URL, 0, len(upstreams))
	for _, u := range upstreams {
		parsed, err := url.Parse(u.URL)
		if err != nil || parsed.Host == "" {
			continue
		}
		urls = append(urls, parsed)
	}
	return &roundRobin{urls: urls}
}

func (rr *roundRobin) len() int { return len(rr.urls) }

func (rr *roundRobin) next() (*url.URL, error) {
	n := len(rr.urls)
	if n == 0 {
		return nil, errNoUpstream
	}
	i := rr.idx.Add(1) - 1
	return rr.urls[i%uint64(n)], nil
}

func routeKey(r routes.Route) string {
	return r.Host + "\x00" + r.PathPrefix + "\x00" + r.Strategy
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
