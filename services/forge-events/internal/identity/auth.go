package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AuthMode controls whether publish/consume require Identity tokens.
type AuthMode string

const (
	AuthModeDev     AuthMode = "dev"
	AuthModeEnforce AuthMode = "enforce"
)

// ParseAuthMode validates FORGE_AUTH_MODE.
func ParseAuthMode(raw string) (AuthMode, error) {
	m := AuthMode(strings.ToLower(strings.TrimSpace(raw)))
	if m == "" {
		return AuthModeDev, nil
	}
	switch m {
	case AuthModeDev, AuthModeEnforce:
		return m, nil
	default:
		return "", fmt.Errorf("FORGE_AUTH_MODE must be enforce|dev, got %q", raw)
	}
}

// Principal is the authenticated caller after introspection.
type Principal struct {
	ID   string
	Type string
}

// Introspector validates bearer tokens via Forge Identity.
type Introspector interface {
	Introspect(ctx context.Context, token string) (Principal, error)
}

type introspectRequest struct {
	Token string `json:"token"`
}

type introspectResponse struct {
	Active        bool   `json:"active"`
	PrincipalType string `json:"principal_type"`
	PrincipalID   string `json:"principal_id"`
}

// HTTPIntrospector calls Identity POST /v1/auth/introspect.
type HTTPIntrospector struct {
	baseURL    string
	client     *http.Client
	cacheTTL   time.Duration
	mu         sync.Mutex
	cache      map[string]cachedPrincipal
	log        *slog.Logger
}

type cachedPrincipal struct {
	p         Principal
	expiresAt time.Time
}

// NewHTTPIntrospector builds an Identity introspect client.
func NewHTTPIntrospector(baseURL string, cacheTTLS int, log *slog.Logger) *HTTPIntrospector {
	if log == nil {
		log = slog.Default()
	}
	ttl := time.Duration(cacheTTLS) * time.Second
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return &HTTPIntrospector{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:   &http.Client{Timeout: 5 * time.Second},
		cacheTTL: ttl,
		cache:    make(map[string]cachedPrincipal),
		log:      log,
	}
}

// Introspect returns the active principal for a token, or ErrUnauthorized.
func (h *HTTPIntrospector) Introspect(ctx context.Context, token string) (Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	if p, ok := h.getCached(token); ok {
		return p, nil
	}
	body, _ := json.Marshal(introspectRequest{Token: token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/auth/introspect", bytes.NewReader(body))
	if err != nil {
		return Principal{}, fmt.Errorf("introspect request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return Principal{}, fmt.Errorf("introspect transport: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Principal{}, fmt.Errorf("introspect status %d", resp.StatusCode)
	}
	var out introspectResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Principal{}, fmt.Errorf("introspect decode: %w", err)
	}
	if !out.Active || strings.TrimSpace(out.PrincipalID) == "" {
		return Principal{}, ErrUnauthorized
	}
	p := Principal{ID: out.PrincipalID, Type: out.PrincipalType}
	h.putCached(token, p)
	return p, nil
}

func (h *HTTPIntrospector) getCached(token string) (Principal, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.cache[token]
	if !ok || time.Now().After(c.expiresAt) {
		return Principal{}, false
	}
	return c.p, true
}

func (h *HTTPIntrospector) putCached(token string, p Principal) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[token] = cachedPrincipal{p: p, expiresAt: time.Now().Add(h.cacheTTL)}
}

// Gate enforces optional auth on Events API handlers.
type Gate struct {
	Mode         AuthMode
	Introspector Introspector
	Log          *slog.Logger
}

// Enforce reports whether tokens are required.
func (g *Gate) Enforce() bool {
	return g != nil && g.Mode == AuthModeEnforce
}

// Authenticate extracts and validates a bearer token when enforce is on.
// In dev mode returns an empty principal without error (unless a token is
// presented and introspection is configured — then it is validated optionally).
func (g *Gate) Authenticate(r *http.Request) (Principal, error) {
	token := bearerToken(r)
	if g == nil || !g.Enforce() {
		if token == "" || g == nil || g.Introspector == nil {
			return Principal{}, nil
		}
		// Dev: ignore introspect failures; still surface principal when valid.
		p, err := g.Introspector.Introspect(r.Context(), token)
		if err != nil {
			return Principal{}, nil
		}
		return p, nil
	}
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	if g.Introspector == nil {
		return Principal{}, fmt.Errorf("identity introspector not configured")
	}
	p, err := g.Introspector.Introspect(r.Context(), token)
	if err != nil {
		if err == ErrUnauthorized {
			return Principal{}, ErrUnauthorized
		}
		if g.Log != nil {
			g.Log.Warn("identity introspect failed", "error", err.Error())
		}
		return Principal{}, ErrUnauthorized
	}
	return p, nil
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
