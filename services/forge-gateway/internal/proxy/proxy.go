package proxy

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"forge.local/services/forge-gateway/internal/httperr"
	"forge.local/services/forge-gateway/internal/routes"
)

var errNoUpstream = errors.New("no upstream")

// Handler dispatches matched requests to upstreams via httputil.ReverseProxy.
type Handler struct {
	table   *routes.Table
	log     *slog.Logger
	pickers sync.Map // routeKey -> *roundRobin
}

// NewHandler returns a data-plane proxy handler bound to table.
func NewHandler(table *routes.Table, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{table: table, log: log}
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
		httperr.Write(w, http.StatusBadGateway, "bad_gateway", "no usable upstream")
		return
	}

	start := time.Now()
	rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.Host = upstream.Host
			// Preserve original path/query; only configured upstreams are targeted.
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			h.log.Warn("upstream error",
				"host", req.Host,
				"path", req.URL.Path,
				"upstream", upstream.String(),
				"error", err.Error(),
			)
			httperr.Write(rw, http.StatusBadGateway, "bad_gateway", "upstream connection error")
		},
	}
	proxy.ServeHTTP(rw, r)

	h.log.Info("proxied request",
		"route_host", route.Host,
		"route_path_prefix", route.PathPrefix,
		"upstream", upstream.String(),
		"status", rw.status,
		"duration_ms", time.Since(start).Milliseconds(),
		"method", r.Method,
		"path", r.URL.Path,
		"host", r.Host,
	)
}

func (h *Handler) pick(route routes.Route) (*url.URL, error) {
	key := routeKey(route)
	v, _ := h.pickers.LoadOrStore(key, newRoundRobin(route.Upstreams))
	rr := v.(*roundRobin)
	// If the route table was replaced with a different upstream set for the same
	// key shape, rebuild the picker when lengths diverge.
	if rr.len() != len(route.Upstreams) {
		rr = newRoundRobin(route.Upstreams)
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
