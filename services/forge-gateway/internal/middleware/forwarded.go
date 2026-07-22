package middleware

import (
	"net"
	"net/http"
	"strings"
)

// Hop-by-hop headers per RFC 7230 §6.1 (plus common extensions).
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// ForwardedOptions controls how forwarded headers are applied.
type ForwardedOptions struct {
	// TrustInboundXFF keeps a client-supplied X-Forwarded-For chain and appends the peer.
	// When false, inbound XFF is discarded and only the observed client IP is set.
	TrustInboundXFF bool
}

// StripHopByHop removes hop-by-hop headers (RFC 7230) from h, including
// any header names listed in Connection.
func StripHopByHop(h http.Header) {
	if h == nil {
		return
	}
	if conn := h.Values("Connection"); len(conn) > 0 {
		for _, v := range conn {
			for _, name := range strings.Split(v, ",") {
				name = textprotoCanonical(strings.TrimSpace(name))
				if name != "" {
					h.Del(name)
				}
			}
		}
	}
	for name := range hopByHopHeaders {
		h.Del(name)
	}
}

// ApplyForwarded sets X-Forwarded-* and RFC 7239 Forwarded on out for upstream,
// reading client identity from in (typically ProxyRequest.In).
// When in is nil, out is used as the identity source (single-request helpers/tests).
func ApplyForwarded(out *http.Request, opts ForwardedOptions) {
	ApplyForwardedFrom(out, out, opts)
}

// ApplyForwardedFrom is like ApplyForwarded but reads client identity from in.
func ApplyForwardedFrom(out, in *http.Request, opts ForwardedOptions) {
	if out == nil {
		return
	}
	if in == nil {
		in = out
	}
	clientIP := clientIPFromRemoteAddr(in.RemoteAddr)
	proto := "http"
	if in.TLS != nil {
		proto = "https"
	}
	host := in.Host
	if host == "" {
		host = in.Header.Get("Host")
	}

	var xff string
	if opts.TrustInboundXFF {
		existing := strings.TrimSpace(in.Header.Get("X-Forwarded-For"))
		if existing != "" && clientIP != "" {
			xff = existing + ", " + clientIP
		} else if existing != "" {
			xff = existing
		} else {
			xff = clientIP
		}
	} else {
		xff = clientIP
	}
	if xff != "" {
		out.Header.Set("X-Forwarded-For", xff)
	} else {
		out.Header.Del("X-Forwarded-For")
	}

	out.Header.Set("X-Forwarded-Proto", proto)
	if host != "" {
		out.Header.Set("X-Forwarded-Host", host)
	}

	// RFC 7239 Forwarded: for=…;proto=…;host=…
	parts := make([]string, 0, 3)
	if clientIP != "" {
		parts = append(parts, "for="+quoteForwardedToken(clientIP))
	}
	parts = append(parts, "proto="+proto)
	if host != "" {
		parts = append(parts, "host="+quoteForwardedToken(host))
	}
	out.Header.Set("Forwarded", strings.Join(parts, ";"))
}

func clientIPFromRemoteAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	// Some stacks pass a bare IP.
	if remoteAddr != "" {
		return remoteAddr
	}
	return ""
}

func quoteForwardedToken(v string) string {
	// Quote when the value contains characters that require it (IPv6, host:port, etc.).
	if strings.ContainsAny(v, "\",=; \t") || strings.Contains(v, ":") {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return v
}

// textprotoCanonical mirrors mime/canonical header key casing without importing textproto in tests.
func textprotoCanonical(s string) string {
	if s == "" {
		return s
	}
	return http.CanonicalHeaderKey(s)
}
